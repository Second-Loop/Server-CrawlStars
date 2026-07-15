package rooms

import (
	"sync/atomic"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

var benchmarkSnapshotPayload []byte

func BenchmarkStoreTickRoomsParallel(b *testing.B) {
	const roomCount = 32
	store := NewStoreWithClock(roomCount+1, newFakeClock())
	b.Cleanup(store.Close)
	rooms := make([]*room, 0, roomCount)
	for range roomCount {
		created, err := store.createRoom()
		if err != nil {
			b.Fatalf("create room: %v", err)
		}
		if _, err := store.addPlayer(created.ID); err != nil {
			b.Fatalf("add player: %v", err)
		}
		if _, err := store.startRoom(created.ID); err != nil {
			b.Fatalf("start room: %v", err)
		}
		rooms = append(rooms, store.lookupRoom(created.ID))
	}

	b.ReportAllocs()
	b.ResetTimer()
	var next atomic.Uint64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			index := next.Add(1) - 1
			store.tickRoomState(rooms[index%roomCount])
		}
	})
}

func BenchmarkBroadcastFanout(b *testing.B) {
	const clientCount = 64
	sessions := make([]*clientSession, 0, clientCount)
	for range clientCount {
		sessions = append(sessions, &clientSession{
			snapshots: make(chan []byte, 1),
			done:      make(chan struct{}),
		})
	}
	message := benchmarkSnapshotMessage()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if !enqueueSnapshotMessage(sessions, message) {
			b.Fatal("marshal snapshot fanout payload")
		}
	}
}

func BenchmarkSnapshotMarshal(b *testing.B) {
	message := benchmarkSnapshotMessage()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		payload, err := marshalMessage(message)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkSnapshotPayload = payload
	}
}

func benchmarkSnapshotMessage() roomSnapshotMessage {
	return roomSnapshotMessage{
		Type: "snapshot",
		Snapshot: roomSnapshotFromSimulation(simulation.Snapshot{
			Tick: 42,
			Players: []simulation.PlayerData{{
				ID:      "player_benchmark",
				Team:    simulation.TeamRed,
				Pos:     simulation.Vector2{X: 3, Y: 7},
				MoveDir: simulation.Vector2{X: 1},
				HP:      simulation.DefaultPlayerHP,
			}},
			Projectiles: []simulation.ProjectileData{{
				ID:      "projectile_benchmark",
				OwnerID: "player_benchmark",
				Pos:     simulation.Vector2{X: 3, Y: 6},
				Dir:     simulation.Vector2{Y: -1},
			}},
		}, MatchStatusStarted),
	}
}
