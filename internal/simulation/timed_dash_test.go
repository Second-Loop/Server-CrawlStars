package simulation

import (
	"encoding/json"
	"testing"
)

func assertDashWire(t *testing.T, player PlayerData, active bool, ready Tick) {
	t.Helper()
	data, err := json.Marshal(player)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["IsDashing"] != active || player.AttackReadyTick != ready {
		t.Fatalf("dash wire = %v ready=%d, want %t/%d", wire["IsDashing"], player.AttackReadyTick, active, ready)
	}
}

func TestTimedDashNineSegmentsLockMovementAttackAndACK(t *testing.T) {
	state := newShellyDashTestState(t, []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly}}, MapData{})
	for tick := 1; tick <= 9; tick++ {
		direction := Vector2{Y: 1}
		if tick == 1 {
			direction = Vector2{X: 1}
		}
		snapshot := state.Step([]InputCommand{{PlayerID: "shelly", ClientTick: int64(tick), MoveDir: Vector2{Y: 1}, AttackDir: direction, PressedAttack: true, PressedSkill: true}})
		player := playerByID(t, snapshot, "shelly")
		assertVectorClose(t, "segment position", player.Pos, Vector2{X: float64(tick) * 0.36}, dashTestTolerance)
		ready := Tick(10)
		if tick == 9 {
			ready = 0
		}
		assertDashWire(t, player, tick < 9, ready)
		if player.PressedAttack || len(snapshot.Projectiles) != 0 || player.LastProcessedClientTick != int64(tick) || player.SkillReadyTick != 361 || player.AttackCharges != 3 || player.PressedSkill != (tick == 1) {
			t.Fatalf("tick %d input gates/reload/ACK = %+v projectiles=%d", tick, player, len(snapshot.Projectiles))
		}
	}
	snapshot := state.Step([]InputCommand{{PlayerID: "shelly", ClientTick: 10, MoveDir: Vector2{Y: 1}, AttackDir: Vector2{Y: 1}, PressedAttack: true}})
	player := playerByID(t, snapshot, "shelly")
	assertVectorClose(t, "post dash movement", player.Pos, Vector2{X: 3.24, Y: DefaultPlayerSpeed * TickDuration}, dashTestTolerance)
	if !player.PressedAttack || player.AttackCharges != 2 || len(snapshot.Projectiles) != 5 {
		t.Fatalf("next step attack rejected: %+v", snapshot)
	}
}

func TestTimedDashContinuesWithoutInputAndStopsPermanentlyOnCollision(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		players := []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly}}
		if blocked {
			players = append(players, PlayerData{ID: "blocker", Pos: Vector2{X: 1.5}})
		}
		state := newShellyDashTestState(t, players, MapData{})
		snapshot := state.Step([]InputCommand{{PlayerID: "shelly", AttackDir: Vector2{X: 1}, PressedSkill: true}})
		for tick := 2; tick <= 12; tick++ {
			snapshot = state.Step(nil)
			if blocked && tick == 2 {
				assertDashWire(t, playerByID(t, snapshot, "shelly"), false, 0)
				state.EliminatePlayers([]PlayerID{"blocker"})
			}
		}
		want := 3.24
		if blocked {
			want = 0.499999
		}
		player := playerByID(t, snapshot, "shelly")
		assertVectorClose(t, "final position", player.Pos, Vector2{X: want}, dashTestTolerance)
		assertDashWire(t, player, false, 0)
	}
}

func TestTimedDashDeathClearsStateIncludingAfterMovement(t *testing.T) {
	for _, kind := range []string{"eliminate", "melee", "projectile"} {
		t.Run(kind, func(t *testing.T) {
			state := newShellyDashTestState(t, []PlayerData{{ID: "shelly", Team: TeamBlue, CharacterType: CharacterTypeShelly, HP: 1}, {ID: "enemy", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: 2}}}, MapData{})
			state.Step([]InputCommand{{PlayerID: "shelly", AttackDir: Vector2{X: 1}, PressedSkill: true}})
			var inputs []InputCommand
			switch kind {
			case "eliminate":
				state.EliminatePlayers([]PlayerID{"shelly"})
			case "melee":
				inputs = []InputCommand{{PlayerID: "enemy", AttackDir: Vector2{X: -1}, PressedAttack: true}}
			case "projectile":
				state.projectiles = []ProjectileData{{ID: "lethal", OwnerID: "enemy", Pos: Vector2{X: 0.36}, Damage: 100, Radius: 0.1}}
			}
			snapshot := state.Step(inputs)
			player := playerByID(t, snapshot, "shelly")
			if !player.IsDead {
				t.Fatalf("expected %s death: %+v", kind, player)
			}
			assertDashWire(t, player, false, 0)
			next := state.Step(nil)
			assertVectorClose(t, "dead position", playerByID(t, next, "shelly").Pos, player.Pos, dashTestTolerance)
		})
	}
}

func TestTimedDashNormalizationClearsUnownedPublicState(t *testing.T) {
	state := NewState([]PlayerData{{ID: "shelly", IsDashing: true, AttackReadyTick: 999}})
	assertDashWire(t, state.players[0], false, 0)
}

func TestTimedDashRejectedActivationKeepsNormalMovement(t *testing.T) {
	for _, cooldown := range []bool{false, true} {
		state := newShellyDashTestState(t, []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly}}, MapData{})
		direction := Vector2{}
		if cooldown {
			state.players[0].SkillReadyTick = 99
			direction = Vector2{X: 1}
		}
		snapshot := state.Step([]InputCommand{{PlayerID: "shelly", MoveDir: Vector2{Y: 1}, AttackDir: direction, PressedSkill: true, PressedAttack: true}})
		player := playerByID(t, snapshot, "shelly")
		assertVectorClose(t, "rejected dash normal movement", player.Pos, Vector2{Y: DefaultPlayerSpeed * TickDuration}, dashTestTolerance)
		assertDashWire(t, player, false, 0)
		if player.PressedSkill || player.PressedAttack != cooldown {
			t.Fatalf("rejected dash fallback = %+v", player)
		}
	}
}
