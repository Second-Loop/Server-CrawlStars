package rooms

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestSlowWriterLatestOnlyDropsExpiredProjectileTombstone(t *testing.T) {
	gameConfig := simulation.StaticGameConfig()
	gameMap := simulation.StaticMapFixture()
	for index := range gameConfig.Player.Types {
		if gameConfig.Player.Types[index].CharacterType != simulation.CharacterTypeShelly {
			continue
		}
		gameConfig.Player.Types[index].NormalAttack.Projectile.Count = 1
		gameConfig.Player.Types[index].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{0}
	}
	state := simulation.NewStateWithConfig([]simulation.PlayerData{{
		ID:   "owner",
		Team: simulation.TeamRed,
		Pos:  gameMap.WorldPos(1, 1),
	}}, simulation.Config{Map: gameMap, Game: gameConfig})

	snapshots := []simulation.Snapshot{state.Step([]simulation.InputCommand{{
		PlayerID:      "owner",
		AttackDir:     simulation.Vector2{X: 1},
		PressedAttack: true,
	}})}
	destroyedTick := simulation.Tick(0)
	for len(snapshots) == 1 || destroyedTick == 0 || snapshots[len(snapshots)-1].Tick < destroyedTick+30 {
		snapshot := state.Step(nil)
		snapshots = append(snapshots, snapshot)
		if destroyedTick == 0 && len(snapshot.Projectiles) == 1 && snapshot.Projectiles[0].IsDestroyed {
			destroyedTick = snapshot.Tick
		}
		if snapshot.Tick > 200 {
			t.Fatalf("projectile did not reach the destroyed/expiry setup: last snapshot=%+v", snapshot)
		}
	}
	if destroyedTick == 0 {
		t.Fatal("expected a destroyed projectile in the simulation snapshots")
	}
	final := snapshots[len(snapshots)-1]
	if final.Tick != destroyedTick+30 || len(final.Projectiles) != 0 {
		t.Fatalf("simulation final snapshot=%+v, want tick %d with no projectile", final, destroyedTick+30)
	}

	conn := newFakeClientConn(true)
	session := newClientSession(conn, nil)
	t.Cleanup(func() {
		session.close(1000, "test complete")
	})

	for index, snapshot := range snapshots {
		payload, err := json.Marshal(roomSnapshotMessage{
			Type:     "snapshot",
			Snapshot: roomSnapshotFromSimulation(snapshot, MatchStatusStarted),
		})
		if err != nil {
			t.Fatalf("marshal snapshot %d: %v", index, err)
		}
		session.enqueueSnapshot(payload)
		if index == 0 {
			select {
			case <-conn.writeStarted:
			case <-time.After(time.Second):
				t.Fatal("expected slow writer to take the first snapshot")
			}
		}
	}
	close(conn.allowWrite)

	select {
	case <-conn.writes:
	case <-time.After(time.Second):
		t.Fatal("expected first in-flight snapshot")
	}
	select {
	case payload := <-conn.writes:
		var message roomSnapshotMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatalf("decode latest snapshot: %v", err)
		}
		if message.Snapshot.Tick != final.Tick {
			t.Fatalf("latest queued snapshot tick=%d, want %d", message.Snapshot.Tick, final.Tick)
		}
		if len(message.Snapshot.Projectiles) != 0 {
			t.Fatalf("latest queued snapshot retained expired projectile: %+v", message.Snapshot.Projectiles)
		}
	case <-time.After(time.Second):
		t.Fatal("expected latest queued snapshot")
	}
}
