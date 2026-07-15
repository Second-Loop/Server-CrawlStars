package simulation

import (
	"math"
	"strings"
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

func TestLoadMapDataReadsJSONFixture(t *testing.T) {
	gameMap, err := LoadMapData(strings.NewReader(`{
		"width": 7,
		"height": 5,
		"index": 2,
		"maxPlayers": 2,
		"tileSize": 1.5,
		"map": [
			[1, 1, 1, 1, 1, 1, 1],
			[1, 0, 0, 0, 0, 0, 1],
			[1, 0, 1, 0, 1, 0, 1],
			[1, 0, 0, 0, 0, 0, 1],
			[1, 1, 1, 1, 1, 1, 1]
		]
	}`))
	if err != nil {
		t.Fatalf("load map data: %v", err)
	}
	if gameMap.Width != 7 || gameMap.Height != 5 {
		t.Fatalf("expected 7x5 fixture, got %dx%d", gameMap.Width, gameMap.Height)
	}
	if gameMap.Index != 2 {
		t.Fatalf("expected map index 2, got %d", gameMap.Index)
	}
	if gameMap.MaxPlayers != 2 {
		t.Fatalf("expected max players 2, got %d", gameMap.MaxPlayers)
	}
	if gameMap.TileSize != 1.5 {
		t.Fatalf("expected tile size 1.5, got %f", gameMap.TileSize)
	}
	if gameMap.Map[2][2] != TileWall || gameMap.Map[1][1] != TileGround {
		t.Fatalf("expected loaded tile values, got %+v", gameMap.Map)
	}
}

func TestLoadMapDataAcceptsBushAndWaterTiles(t *testing.T) {
	gameMap, err := LoadMapData(strings.NewReader("{" +
		"\"width\":4,\"height\":4,\"index\":0,\"maxPlayers\":2,\"tileSize\":1.2," +
		"\"map\":[[1,1,1,1],[1,3,4,1],[1,0,2,1],[1,1,1,1]]}"))
	if err != nil {
		t.Fatalf("load bush/water map data: %v", err)
	}
	if gameMap.Map[1][1] != TileBush || gameMap.Map[1][2] != TileWater {
		t.Fatalf("expected bush/water tiles, got %+v", gameMap.Map[1])
	}
}

func TestLoadMapDataRejectsTileOutsideContract(t *testing.T) {
	_, err := LoadMapData(strings.NewReader("{" +
		"\"width\":4,\"height\":4,\"index\":0,\"maxPlayers\":2,\"tileSize\":1.2," +
		"\"map\":[[1,1,1,1],[1,0,5,1],[1,0,2,1],[1,1,1,1]]}"))
	if err == nil {
		t.Fatal("expected tile value 5 to be rejected")
	}
}

func TestLoadMapDataRejectsInvalidTileGrid(t *testing.T) {
	_, err := LoadMapData(strings.NewReader(`{
		"width": 4,
		"height": 4,
		"index": 0,
		"maxPlayers": 2,
		"tileSize": 1.2,
		"map": [
			[1, 1, 1, 1],
			[1, 0, 0, 1],
			[1, 0, 99, 1],
			[1, 1, 1, 1]
		]
	}`))
	if err == nil {
		t.Fatal("expected invalid tile value to be rejected")
	}
}

func TestLoadDefaultMapFixtureUsesCommittedFixturePath(t *testing.T) {
	gameMap, err := LoadDefaultMapFixture()
	if err != nil {
		t.Fatalf("load default map fixture: %v", err)
	}
	if DefaultMapFixturePath != "internal/simulation/fixtures/default-map.json" {
		t.Fatalf("unexpected default map fixture path %q", DefaultMapFixturePath)
	}
	if gameMap.Width <= 0 || gameMap.Height <= 0 {
		t.Fatalf("expected positive default fixture size, got %dx%d", gameMap.Width, gameMap.Height)
	}
	if len(gameMap.Map) != gameMap.Height {
		t.Fatalf("expected default fixture rows %d, got %d", gameMap.Height, len(gameMap.Map))
	}
	for y, row := range gameMap.Map {
		if len(row) != gameMap.Width {
			t.Fatalf("expected default fixture row %d width %d, got %d", y, gameMap.Width, len(row))
		}
	}
	if gameMap.MaxPlayers <= 0 {
		t.Fatalf("expected positive default fixture max players, got %d", gameMap.MaxPlayers)
	}
	if gameMap.TileSize <= 0 {
		t.Fatalf("expected positive default fixture tile size, got %f", gameMap.TileSize)
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
		if player.HP != DefaultPlayerHP {
			t.Fatalf("expected default HP %f, got %f", DefaultPlayerHP, player.HP)
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

func TestStepKeepsTeamAndSlotWithoutApplyingMatchRules(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed, Slot: 0},
		{ID: PlayerID("blue-1"), Team: TeamBlue, Slot: 0},
		{ID: PlayerID("red-2"), Team: TeamRed, Slot: 1},
		{ID: PlayerID("blue-2"), Team: TeamBlue, Slot: 1},
	})

	snapshot := state.Step(nil)

	if len(snapshot.Players) != 4 {
		t.Fatalf("expected 4 players in snapshot, got %d", len(snapshot.Players))
	}
	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{})
	assertPlayer(t, snapshot, PlayerID("blue-1"), TeamBlue, 0, Vector2{})
	assertPlayer(t, snapshot, PlayerID("red-2"), TeamRed, 1, Vector2{})
	assertPlayer(t, snapshot, PlayerID("blue-2"), TeamBlue, 1, Vector2{})
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

func TestStepClampsDiagonalMovementWhenOtherAxisHitsWall(t *testing.T) {
	component := 1 / math.Sqrt(2)
	stepDistance := DefaultPlayerSpeed * TickDuration * component
	start := Vector2{
		X: StaticMapFixture().WorldPos(0, 2).X + TileSize/2 + DefaultPlayerRadius + stepDistance,
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

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{X: start.X, Y: start.Y + stepDistance})
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

func TestStepClampsOversizedMovementDirection(t *testing.T) {
	oversizedState := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})
	unitState := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})

	oversized := oversizedState.Step([]InputCommand{{
		PlayerID: PlayerID("red-1"),
		MoveDir:  Vector2{X: 100},
	}})
	unit := unitState.Step([]InputCommand{{
		PlayerID: PlayerID("red-1"),
		MoveDir:  Vector2{X: 1},
	}})

	assertVector(t, "clamped position", oversized.Players[0].Pos, unit.Players[0].Pos)
	assertVector(t, "clamped movement direction", oversized.Players[0].MoveDir, unit.Players[0].MoveDir)
}

func TestStepClampsDiagonalMovementDirection(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})

	snapshot := state.Step([]InputCommand{{
		PlayerID: PlayerID("red-1"),
		MoveDir:  Vector2{X: 1, Y: 1},
	}})

	component := 1 / math.Sqrt(2)
	wantDirection := Vector2{X: component, Y: component}
	wantPosition := Vector2{
		X: DefaultPlayerSpeed * TickDuration * component,
		Y: DefaultPlayerSpeed * TickDuration * component,
	}
	assertVector(t, "diagonal movement direction", snapshot.Players[0].MoveDir, wantDirection)
	assertVector(t, "diagonal position", snapshot.Players[0].Pos, wantPosition)
}

func TestStepClampsExtremeFiniteMovementDirection(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})

	snapshot := state.Step([]InputCommand{{
		PlayerID: PlayerID("red-1"),
		MoveDir:  Vector2{X: math.MaxFloat64, Y: math.MaxFloat64},
	}})

	component := 1 / math.Sqrt(2)
	wantDirection := Vector2{X: component, Y: component}
	wantPosition := Vector2{
		X: DefaultPlayerSpeed * TickDuration * component,
		Y: DefaultPlayerSpeed * TickDuration * component,
	}
	assertVector(t, "extreme finite movement direction", snapshot.Players[0].MoveDir, wantDirection)
	assertVector(t, "extreme finite position", snapshot.Players[0].Pos, wantPosition)
}

func TestStepClampPreservesAnalogMovementDirection(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})
	direction := Vector2{X: 0.25}

	snapshot := state.Step([]InputCommand{{
		PlayerID: PlayerID("red-1"),
		MoveDir:  direction,
	}})

	assertVector(t, "analog movement direction", snapshot.Players[0].MoveDir, direction)
	assertVector(t, "analog position", snapshot.Players[0].Pos, Vector2{
		X: DefaultPlayerSpeed * TickDuration * direction.X,
	})
}

func TestStepNormalizesOversizedAttackDirection(t *testing.T) {
	oversizedState := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})
	unitState := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})

	oversized := oversizedState.Step([]InputCommand{{
		PlayerID:      PlayerID("red-1"),
		AttackDir:     Vector2{Y: 50},
		PressedAttack: true,
	}})
	unit := unitState.Step([]InputCommand{{
		PlayerID:      PlayerID("red-1"),
		AttackDir:     Vector2{Y: 1},
		PressedAttack: true,
	}})

	assertVector(t, "normalized attack direction", oversized.Players[0].AttackDir, unit.Players[0].AttackDir)
	assertVector(t, "normalized projectile direction", oversized.Projectiles[0].Dir, unit.Projectiles[0].Dir)
}

func TestStepNormalizesExtremeFiniteAttackDirection(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})

	snapshot := state.Step([]InputCommand{{
		PlayerID:      PlayerID("red-1"),
		AttackDir:     Vector2{X: math.MaxFloat64, Y: math.MaxFloat64},
		PressedAttack: true,
	}})

	component := 1 / math.Sqrt(2)
	wantDirection := Vector2{X: component, Y: component}
	assertVector(t, "extreme finite attack direction", snapshot.Players[0].AttackDir, wantDirection)
	if len(snapshot.Projectiles) != 1 {
		t.Fatalf("expected extreme finite attack direction to create 1 projectile, got %d", len(snapshot.Projectiles))
	}
	assertVector(t, "extreme finite projectile direction", snapshot.Projectiles[0].Dir, wantDirection)
}

func TestStepDeadPlayerInputIsIgnoredAfterProjectileHit(t *testing.T) {
	shooterPosition := Vector2{}
	targetPosition := Vector2{X: DefaultProjectileSpeed * TickDuration}
	state := NewState([]PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed, Pos: shooterPosition},
		{ID: PlayerID("blue-1"), Team: TeamBlue, Pos: targetPosition, HP: DefaultProjectileDamage},
	})

	state.Step([]InputCommand{
		{PlayerID: PlayerID("red-1"), AttackDir: Vector2{X: 1}, PressedAttack: true},
		{PlayerID: PlayerID("blue-1"), AttackDir: Vector2{X: 1}, PressedAttack: true},
	})
	snapshot := state.Step([]InputCommand{{
		PlayerID:      PlayerID("blue-1"),
		MoveDir:       Vector2{Y: 1},
		AttackDir:     Vector2{X: -1},
		PressedAttack: true,
	}})

	target := snapshot.Players[1]
	if !target.IsDead || target.HP != 0 {
		t.Fatalf("expected target to be dead with zero HP, got HP=%f IsDead=%t", target.HP, target.IsDead)
	}
	assertVector(t, "dead player position", target.Pos, targetPosition)
	assertVector(t, "dead player movement direction", target.MoveDir, Vector2{})
	assertVector(t, "dead player attack direction", target.AttackDir, Vector2{X: 1})
	if target.PressedAttack {
		t.Fatal("expected dead player PressedAttack to be false")
	}
	if got := len(snapshot.Projectiles); got != 2 {
		t.Fatalf("expected dead input to create no projectile, got %d projectiles", got)
	}
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

func TestStepEnforcesAttackChargeCapacity(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})

	for attack := 0; attack < 4; attack++ {
		snapshot := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
		if !snapshot.Players[0].PressedAttack {
			t.Fatalf("expected attack %d to be accepted", attack+1)
		}
	}
	exhausted := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})

	if got := len(exhausted.Projectiles); got != 4 {
		t.Fatalf("expected exhausted fifth attack to be ignored, got %d projectiles", got)
	}
	if exhausted.Players[0].PressedAttack {
		t.Fatal("expected exhausted fifth attack to leave PressedAttack false")
	}
}

func TestStepRestoresAttackChargeAfterRechargeTicks(t *testing.T) {
	gameConfig := StaticGameConfig()
	gameConfig.Player.Types[0].MaxAttackCharges = 1
	gameConfig.Map = MapData{}
	state := NewStateWithConfig([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}}, Config{Game: gameConfig})

	state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
	for tick := 0; tick < 28; tick++ {
		state.Step(nil)
	}
	notYetRecharged := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
	if got := len(notYetRecharged.Projectiles); got != 1 {
		t.Fatalf("expected no recharge before 30 ticks, got %d projectiles", got)
	}
	if notYetRecharged.Players[0].PressedAttack {
		t.Fatal("expected attack before recharge completion to be ignored")
	}

	recharged := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
	if got := len(recharged.Projectiles); got != 2 {
		t.Fatalf("expected one restored charge after 30 ticks, got %d projectiles", got)
	}
	if !recharged.Players[0].PressedAttack {
		t.Fatal("expected attack after recharge completion to be accepted")
	}
}

func TestStepAttackChargeDoesNotAccumulateAboveMaximum(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})
	for tick := 0; tick < 120; tick++ {
		state.Step(nil)
	}

	for attack := 0; attack < 5; attack++ {
		state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
	}
	snapshot := state.Step(nil)

	if got := len(snapshot.Projectiles); got != 4 {
		t.Fatalf("expected charge capacity to remain capped at 4, got %d projectiles", got)
	}
}

func TestStepZeroDirectionDoesNotConsumeAttackCharge(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})

	zeroDirection := state.Step([]InputCommand{{
		PlayerID:      PlayerID("red-1"),
		PressedAttack: true,
	}})
	if zeroDirection.Players[0].PressedAttack {
		t.Fatal("expected zero attack direction to leave PressedAttack false")
	}
	if got := len(zeroDirection.Projectiles); got != 0 {
		t.Fatalf("expected zero attack direction to create no projectile, got %d", got)
	}

	for attack := 0; attack < 4; attack++ {
		state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
	}
	exhausted := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
	if got := len(exhausted.Projectiles); got != 4 {
		t.Fatalf("expected zero direction to consume no charge, got %d projectiles", got)
	}
}

func TestStepKeepsAttackChargesSeparatePerPlayer(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed},
		{ID: PlayerID("blue-1"), Team: TeamBlue},
	})

	for attack := 0; attack < 4; attack++ {
		state.Step([]InputCommand{
			attackInput(PlayerID("red-1")),
			attackInput(PlayerID("blue-1")),
		})
	}
	exhausted := state.Step([]InputCommand{
		attackInput(PlayerID("red-1")),
		attackInput(PlayerID("blue-1")),
	})

	if got := len(exhausted.Projectiles); got != 8 {
		t.Fatalf("expected two independent four-charge budgets, got %d projectiles", got)
	}
	if exhausted.Players[0].PressedAttack || exhausted.Players[1].PressedAttack {
		t.Fatal("expected both exhausted attacks to leave PressedAttack false")
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

func TestStepProjectileHitReducesTargetHPAndDestroysProjectile(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	target := Vector2{
		X: start.X + DefaultProjectileSpeed*TickDuration,
		Y: start.Y,
	}
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
		{
			ID:   PlayerID("blue-1"),
			Team: TeamBlue,
			Slot: 0,
			Pos:  target,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	state.Step([]InputCommand{
		{
			PlayerID:      PlayerID("red-1"),
			AttackDir:     Vector2{X: 1, Y: 0},
			PressedAttack: true,
		},
	})
	snapshot := state.Step(nil)

	assertPlayerHP(t, snapshot, PlayerID("red-1"), DefaultPlayerHP, false)
	assertPlayerHP(t, snapshot, PlayerID("blue-1"), DefaultPlayerHP-DefaultProjectileDamage, false)
	if len(snapshot.Projectiles) != 1 {
		t.Fatalf("expected 1 projectile, got %d", len(snapshot.Projectiles))
	}
	if !snapshot.Projectiles[0].IsDestroyed {
		t.Fatal("expected projectile to be destroyed after hitting player")
	}

	next := state.Step(nil)

	assertPlayerHP(t, next, PlayerID("blue-1"), DefaultPlayerHP-DefaultProjectileDamage, false)
	if !next.Projectiles[0].IsDestroyed {
		t.Fatal("expected hit projectile to stay destroyed")
	}
}

func TestStepProjectileDoesNotSelfHitOwner(t *testing.T) {
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

	state.Step([]InputCommand{
		{
			PlayerID:      PlayerID("red-1"),
			AttackDir:     Vector2{X: 1, Y: 0},
			PressedAttack: true,
		},
	})
	snapshot := state.Step(nil)

	assertPlayerHP(t, snapshot, PlayerID("red-1"), DefaultPlayerHP, false)
	if len(snapshot.Projectiles) != 1 {
		t.Fatalf("expected 1 projectile, got %d", len(snapshot.Projectiles))
	}
	if snapshot.Projectiles[0].IsDestroyed {
		t.Fatal("expected owner-overlapping projectile to remain active")
	}
}

func TestStepProjectileHitMarksTargetDeadWhenHPReachesZero(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	target := Vector2{
		X: start.X + DefaultProjectileSpeed*TickDuration,
		Y: start.Y,
	}
	state := NewStateWithConfig([]PlayerData{
		{
			ID:   PlayerID("red-1"),
			Team: TeamRed,
			Slot: 0,
			Pos:  start,
		},
		{
			ID:   PlayerID("blue-1"),
			Team: TeamBlue,
			Slot: 0,
			Pos:  target,
			HP:   DefaultProjectileDamage,
		},
	}, Config{
		Map: StaticMapFixture(),
	})

	state.Step([]InputCommand{
		{
			PlayerID:      PlayerID("red-1"),
			AttackDir:     Vector2{X: 1, Y: 0},
			PressedAttack: true,
		},
	})
	snapshot := state.Step(nil)

	assertPlayerHP(t, snapshot, PlayerID("blue-1"), 0, true)
	if !snapshot.Projectiles[0].IsDestroyed {
		t.Fatal("expected lethal projectile to be destroyed after hit")
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

func assertPlayerHP(t *testing.T, snapshot Snapshot, id PlayerID, hp float64, isDead bool) {
	t.Helper()

	for _, player := range snapshot.Players {
		if player.ID != id {
			continue
		}
		if player.HP != hp {
			t.Fatalf("expected %s HP %f, got %f", id, hp, player.HP)
		}
		if player.IsDead != isDead {
			t.Fatalf("expected %s IsDead %t, got %t", id, isDead, player.IsDead)
		}
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

func attackInput(playerID PlayerID) InputCommand {
	return InputCommand{
		PlayerID:      playerID,
		AttackDir:     Vector2{X: 1},
		PressedAttack: true,
	}
}
