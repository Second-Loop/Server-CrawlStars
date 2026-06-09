package simulation

import (
	"math"
	"testing"
)

const positionEpsilon = 0.000001

func TestStepReturnsSnapshotWithoutTransport(t *testing.T) {
	state := NewState([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  Vector2{X: -1, Y: 0},
		},
		{
			ID:   PlayerID("blue-1"),
			Team: TeamBlue,
			Slot: 0,
			Pos:  Vector2{X: 1, Y: 0},
		},
	})

	snapshot := state.Step(nil)

	if snapshot.Tick != Tick(1) {
		t.Fatalf("expected first snapshot tick 1, got %d", snapshot.Tick)
	}
	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{X: -1, Y: 0})
	assertPlayer(t, snapshot, PlayerID("blue-1"), TeamBlue, 0, Vector2{X: 1, Y: 0})
}

func TestStepContractAppliesInputCommands(t *testing.T) {
	state := NewState([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  Vector2{X: 0, Y: 0},
		},
	})

	inputs := []InputCommand{
		{
			PlayerID: PlayerID("red-1"),
			MoveDir:  Vector2{X: 1, Y: 0},
		},
	}

	snapshot := state.Step(inputs)

	if snapshot.Tick != Tick(1) {
		t.Fatalf("expected first snapshot tick 1, got %d", snapshot.Tick)
	}
	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{X: DefaultPlayerSpeed * TickDuration, Y: 0})
}

func TestStaticMapFixtureMatchesClientPrototypeValues(t *testing.T) {
	gameMap := StaticMapFixture()

	if gameMap.Width != 5 || gameMap.Height != 5 {
		t.Fatalf("expected 5x5 fixture, got %dx%d", gameMap.Width, gameMap.Height)
	}
	if gameMap.Index != 0 {
		t.Fatalf("expected map index 0, got %d", gameMap.Index)
	}
	if gameMap.MaxPlayers != 6 {
		t.Fatalf("expected max players 6, got %d", gameMap.MaxPlayers)
	}
	if gameMap.TileSize != TileSize {
		t.Fatalf("expected tile size %f, got %f", TileSize, gameMap.TileSize)
	}
	if gameMap.Map[1][1] != TileGround {
		t.Fatalf("expected open tile to use TileGround, got %d", gameMap.Map[1][1])
	}
	if gameMap.Map[0][0] != TileWall {
		t.Fatalf("expected boundary tile to use TileWall, got %d", gameMap.Map[0][0])
	}
}

func TestNewStateUsesClientPlayerDefaults(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed, Slot: 0},
	})

	snapshot := state.Step(nil)

	for _, player := range snapshot.Players {
		if player.ID != PlayerID("red-1") {
			continue
		}
		if player.Speed != DefaultPlayerSpeed {
			t.Fatalf("expected default speed %f, got %f", DefaultPlayerSpeed, player.Speed)
		}
		if player.Radius != DefaultPlayerRadius {
			t.Fatalf("expected default radius %f, got %f", DefaultPlayerRadius, player.Radius)
		}
		return
	}

	t.Fatal("expected snapshot to include red-1")
}

func TestTeamSlotsAreNotLimitedToOnePlayerPerTeam(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed, Slot: 0},
		{ID: PlayerID("red-2"), Team: TeamRed, Slot: 1},
		{ID: PlayerID("blue-1"), Team: TeamBlue, Slot: 0},
	})

	snapshot := state.Step(nil)

	if len(snapshot.Players) != 3 {
		t.Fatalf("expected 3 players in snapshot, got %d", len(snapshot.Players))
	}
	assertPlayer(t, snapshot, PlayerID("red-2"), TeamRed, 1, Vector2{})
}

func TestSnapshotDoesNotExposeMutableState(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed, Slot: 0},
	})

	first := state.Step(nil)
	first.Players[0].Pos = Vector2{X: 99, Y: 99}

	second := state.Step(nil)

	if second.Tick != Tick(2) {
		t.Fatalf("expected second snapshot tick 2, got %d", second.Tick)
	}
	assertPlayer(t, second, PlayerID("red-1"), TeamRed, 0, Vector2{})
}

func TestNewStateDoesNotExposeInitialPlayerSlice(t *testing.T) {
	players := []PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed, Slot: 0},
	}

	state := NewState(players)
	players[0].Pos = Vector2{X: 99, Y: 99}

	snapshot := state.Step(nil)

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{})
}

func TestStateStartsWithStaticMapFixtureAndStepsWithoutInput(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step(nil)

	if snapshot.Tick != Tick(1) {
		t.Fatalf("expected first snapshot tick 1, got %d", snapshot.Tick)
	}
	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, start)
}

func TestStepAppliesMovementInput(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step([]InputCommand{
		{PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: 1, Y: 0}},
	})

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{X: start.X + DefaultPlayerSpeed*TickDuration, Y: start.Y})
}

func TestStepKeepsPlayerPositionWhenMovementHitsWall(t *testing.T) {
	start := Vector2{
		X: StaticMapFixture().WorldPos(0, 1).X + TileSize/2 + DefaultPlayerRadius + DefaultPlayerSpeed*TickDuration,
		Y: StaticMapFixture().WorldPos(1, 1).Y,
	}
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step([]InputCommand{
		{PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: -1, Y: 0}},
	})

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, start)
}

func TestStepAppliesUnblockedAxisWhenOtherAxisHitsWall(t *testing.T) {
	start := Vector2{
		X: StaticMapFixture().WorldPos(0, 2).X + TileSize/2 + DefaultPlayerRadius + DefaultPlayerSpeed*TickDuration,
		Y: StaticMapFixture().WorldPos(1, 2).Y,
	}
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step([]InputCommand{
		{PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: -1, Y: 1}},
	})

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{X: start.X, Y: start.Y + DefaultPlayerSpeed*TickDuration})
}

func TestStepAllowsMovementWhenPlayerCircleStaysOutsideWall(t *testing.T) {
	start := Vector2{
		X: StaticMapFixture().WorldPos(0, 1).X + TileSize/2 + DefaultPlayerRadius + 0.001 + DefaultPlayerSpeed*TickDuration,
		Y: StaticMapFixture().WorldPos(1, 1).Y,
	}
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step([]InputCommand{
		{PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: -1, Y: 0}},
	})

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{X: start.X - DefaultPlayerSpeed*TickDuration, Y: start.Y})
}

func TestStepTreatsTangentWallContactAsCollision(t *testing.T) {
	start := Vector2{
		X: StaticMapFixture().WorldPos(0, 1).X + TileSize/2 + DefaultPlayerRadius + DefaultPlayerSpeed*TickDuration,
		Y: StaticMapFixture().WorldPos(1, 1).Y,
	}
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step([]InputCommand{
		{PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: -1, Y: 0}},
	})

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, start)
}

func TestStepKeepsPlayerPositionWhenPlayerCircleOverlapsWall(t *testing.T) {
	start := Vector2{
		X: StaticMapFixture().WorldPos(0, 1).X + TileSize/2 + DefaultPlayerRadius - 0.001 + DefaultPlayerSpeed*TickDuration,
		Y: StaticMapFixture().WorldPos(1, 1).Y,
	}
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step([]InputCommand{
		{PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: -1, Y: 0}},
	})

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, start)
}

func TestStepIgnoresNonFiniteMovementInput(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step([]InputCommand{
		{PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: math.NaN(), Y: 1}},
	})

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, start)
}

func TestStepAcceptsAttackInputAndAddsProjectileSkeletonToSnapshot(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step([]InputCommand{
		{
			PlayerID:      PlayerID("red-1"),
			AttackDir:     Vector2{X: 1, Y: 0},
			PressedAttack: true,
		},
	})

	if len(snapshot.Projectiles) != 1 {
		t.Fatalf("expected 1 projectile, got %d", len(snapshot.Projectiles))
	}
	projectile := snapshot.Projectiles[0]
	if projectile.ID == "" {
		t.Fatal("expected projectile ID to be set")
	}
	if projectile.OwnerID != PlayerID("red-1") {
		t.Fatalf("expected projectile owner %q, got %q", PlayerID("red-1"), projectile.OwnerID)
	}
	assertVector(t, "projectile position", projectile.Pos, start)
	assertVector(t, "projectile direction", projectile.Dir, Vector2{X: 1, Y: 0})
	if projectile.Speed != DefaultProjectileSpeed {
		t.Fatalf("expected projectile speed %f, got %f", DefaultProjectileSpeed, projectile.Speed)
	}
	if projectile.Damage != DefaultProjectileDamage {
		t.Fatalf("expected projectile damage %f, got %f", DefaultProjectileDamage, projectile.Damage)
	}
	if projectile.Radius != DefaultProjectileRadius {
		t.Fatalf("expected projectile radius %f, got %f", DefaultProjectileRadius, projectile.Radius)
	}
	if projectile.IsDestroyed {
		t.Fatal("expected new projectile to start not destroyed")
	}
}

func TestStepDoesNotCreateProjectileWhenAttackIsNotPressed(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed, Slot: 0},
	})

	snapshot := state.Step([]InputCommand{
		{
			PlayerID:      PlayerID("red-1"),
			AttackDir:     Vector2{X: 1, Y: 0},
			PressedAttack: false,
		},
	})

	if len(snapshot.Projectiles) != 0 {
		t.Fatalf("expected no projectiles, got %d", len(snapshot.Projectiles))
	}
}

func TestStepProcessesMovementAndAttackInSameTick(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	snapshot := state.Step([]InputCommand{
		{
			PlayerID:      PlayerID("red-1"),
			MoveDir:       Vector2{X: 1, Y: 0},
			AttackDir:     Vector2{X: 0, Y: 1},
			PressedAttack: true,
		},
	})

	moved := Vector2{X: start.X + DefaultPlayerSpeed*TickDuration, Y: start.Y}
	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, moved)
	assertPlayerInput(t, snapshot, PlayerID("red-1"), Vector2{X: 1, Y: 0}, Vector2{X: 0, Y: 1}, true)
	if len(snapshot.Projectiles) != 1 {
		t.Fatalf("expected 1 projectile, got %d", len(snapshot.Projectiles))
	}
	assertVector(t, "projectile position", snapshot.Projectiles[0].Pos, moved)
	assertVector(t, "projectile direction", snapshot.Projectiles[0].Dir, Vector2{X: 0, Y: 1})
}

func TestStepMovesExistingProjectileOnNextTick(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	created := state.Step([]InputCommand{
		{
			PlayerID:      PlayerID("red-1"),
			AttackDir:     Vector2{X: 1, Y: 0},
			PressedAttack: true,
		},
	})
	assertVector(t, "new projectile position", created.Projectiles[0].Pos, start)

	moved := state.Step(nil)

	if len(moved.Projectiles) != 1 {
		t.Fatalf("expected 1 projectile, got %d", len(moved.Projectiles))
	}
	assertVector(t, "moved projectile position", moved.Projectiles[0].Pos, Vector2{
		X: start.X + DefaultProjectileSpeed*TickDuration,
		Y: start.Y,
	})
	if moved.Projectiles[0].IsDestroyed {
		t.Fatal("expected projectile to remain active after open movement")
	}
}

func TestStepDestroysProjectileWhenItHitsWall(t *testing.T) {
	gameMap := StaticMapFixture()
	wallCenter := gameMap.WorldPos(4, 1)
	wallMinX := wallCenter.X - TileSize/2
	start := Vector2{
		X: wallMinX - DefaultProjectileRadius - DefaultProjectileSpeed*TickDuration + 0.001,
		Y: wallCenter.Y,
	}
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: gameMap,
	})

	state.Step([]InputCommand{
		{
			PlayerID:      PlayerID("red-1"),
			AttackDir:     Vector2{X: 1, Y: 0},
			PressedAttack: true,
		},
	})
	destroyed := state.Step(nil)

	if len(destroyed.Projectiles) != 1 {
		t.Fatalf("expected 1 projectile, got %d", len(destroyed.Projectiles))
	}
	if !destroyed.Projectiles[0].IsDestroyed {
		t.Fatal("expected projectile to be destroyed after hitting wall")
	}
	destroyedPosition := destroyed.Projectiles[0].Pos

	afterDestroyed := state.Step(nil)

	if len(afterDestroyed.Projectiles) != 1 {
		t.Fatalf("expected destroyed projectile to remain in snapshot, got %d", len(afterDestroyed.Projectiles))
	}
	if !afterDestroyed.Projectiles[0].IsDestroyed {
		t.Fatal("expected projectile to stay destroyed")
	}
	assertVector(t, "destroyed projectile position", afterDestroyed.Projectiles[0].Pos, destroyedPosition)
}

func TestStepDestroysProjectileWhenItLeavesMapBounds(t *testing.T) {
	gameMap := StaticMapFixture()
	mapMaxX := gameMap.WorldPos(gameMap.Width-1, 0).X + TileSize/2
	start := Vector2{
		X: mapMaxX - DefaultProjectileRadius - DefaultProjectileSpeed*TickDuration + 0.001,
		Y: gameMap.WorldPos(3, 1).Y,
	}
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
	}, Config{
		Map: gameMap,
	})

	state.Step([]InputCommand{
		{
			PlayerID:      PlayerID("red-1"),
			AttackDir:     Vector2{X: 1, Y: 0},
			PressedAttack: true,
		},
	})
	snapshot := state.Step(nil)

	if len(snapshot.Projectiles) != 1 {
		t.Fatalf("expected 1 projectile, got %d", len(snapshot.Projectiles))
	}
	if !snapshot.Projectiles[0].IsDestroyed {
		t.Fatal("expected projectile to be destroyed after leaving map bounds")
	}
}

func assertPlayer(t *testing.T, snapshot Snapshot, id PlayerID, team Team, slot int, position Vector2) {
	t.Helper()

	for _, player := range snapshot.Players {
		if player.ID != id {
			continue
		}
		if player.Team != team {
			t.Fatalf("expected %s team %q, got %q", id, team, player.Team)
		}
		if player.Slot != slot {
			t.Fatalf("expected %s slot %d, got %d", id, slot, player.Slot)
		}
		assertVector(t, string(id)+" position", player.Pos, position)
		return
	}

	t.Fatalf("expected snapshot to include player %s", id)
}

func assertPlayerInput(t *testing.T, snapshot Snapshot, id PlayerID, moveDir Vector2, attackDir Vector2, pressedAttack bool) {
	t.Helper()

	for _, player := range snapshot.Players {
		if player.ID != id {
			continue
		}
		assertVector(t, string(id)+" move direction", player.MoveDir, moveDir)
		assertVector(t, string(id)+" attack direction", player.AttackDir, attackDir)
		if player.PressedAttack != pressedAttack {
			t.Fatalf("expected %s pressed attack %t, got %t", id, pressedAttack, player.PressedAttack)
		}
		return
	}

	t.Fatalf("expected snapshot to include player %s", id)
}

func assertVector(t *testing.T, label string, got Vector2, want Vector2) {
	t.Helper()

	if math.Abs(got.X-want.X) > positionEpsilon || math.Abs(got.Y-want.Y) > positionEpsilon {
		t.Fatalf("expected %s %+v, got %+v", label, want, got)
	}
}
