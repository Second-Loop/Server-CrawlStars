package simulation

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

const positionEpsilon = 0.000001

func defaultShellyProjectileDamage() float64 {
	return StaticGameConfig().DefaultPlayerType().NormalAttack.DamagePerHit
}

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

func TestStatePreservesBotIdentity(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: PlayerID("human"), Team: TeamRed, IsBot: false},
		{ID: PlayerID("bot"), Team: TeamBlue, IsBot: true},
	})

	snapshot := state.Step(nil)
	want := map[PlayerID]bool{
		PlayerID("human"): false,
		PlayerID("bot"):   true,
	}
	if len(snapshot.Players) != len(want) {
		t.Fatalf("expected %d players, got %d", len(want), len(snapshot.Players))
	}
	for _, player := range snapshot.Players {
		wantIsBot, ok := want[player.ID]
		if !ok {
			t.Fatalf("unexpected player %q", player.ID)
		}
		if player.IsBot != wantIsBot {
			t.Fatalf("expected player %q IsBot %t, got %t", player.ID, wantIsBot, player.IsBot)
		}
	}
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

func TestStepAcknowledgesProcessedClientTick(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red"), Team: TeamRed}})

	snapshot := state.Step([]InputCommand{{
		PlayerID:   PlayerID("red"),
		ClientTick: 12,
		MoveDir:    Vector2{X: 1},
	}})

	if got := snapshot.Players[0].LastProcessedClientTick; got != 12 {
		t.Fatalf("ACK=%d want=12", got)
	}
}

func TestStepPreservesLastProcessedClientTickWithoutInput(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red"), Team: TeamRed}})
	state.Step([]InputCommand{{PlayerID: PlayerID("red"), ClientTick: 12}})

	snapshot := state.Step(nil)

	if got := snapshot.Players[0].LastProcessedClientTick; got != 12 {
		t.Fatalf("ACK after no input=%d want=12", got)
	}
}

func TestStepTracksClientTickIndependentlyPerPlayer(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: PlayerID("red"), Team: TeamRed},
		{ID: PlayerID("blue"), Team: TeamBlue},
	})
	state.Step([]InputCommand{
		{PlayerID: PlayerID("red"), ClientTick: 7},
		{PlayerID: PlayerID("blue"), ClientTick: 11},
	})

	snapshot := state.Step([]InputCommand{{PlayerID: PlayerID("red"), ClientTick: 8}})

	if got := playerByID(t, snapshot, PlayerID("red")).LastProcessedClientTick; got != 8 {
		t.Fatalf("red ACK=%d want=8", got)
	}
	if got := playerByID(t, snapshot, PlayerID("blue")).LastProcessedClientTick; got != 11 {
		t.Fatalf("blue ACK=%d want=11", got)
	}
}

func TestStepAcknowledgesProcessedInputWithoutVisibleEffect(t *testing.T) {
	t.Run("wall collision", func(t *testing.T) {
		start := Vector2{
			X: StaticMapFixture().WorldPos(0, 1).X + TileSize/2 + DefaultPlayerRadius + DefaultPlayerSpeed*TickDuration,
			Y: StaticMapFixture().WorldPos(1, 1).Y,
		}
		state := NewStateWithConfig([]PlayerData{{
			ID: PlayerID("red"), Team: TeamRed, Pos: start,
		}}, Config{Map: StaticMapFixture()})

		snapshot := state.Step([]InputCommand{{
			PlayerID: PlayerID("red"), ClientTick: 1, MoveDir: Vector2{X: -1},
		}})

		assertVector(t, "wall-blocked position", snapshot.Players[0].Pos, start)
		if got := snapshot.Players[0].LastProcessedClientTick; got != 1 {
			t.Fatalf("wall-blocked ACK=%d want=1", got)
		}
	})

	t.Run("zero attack direction", func(t *testing.T) {
		state := NewState([]PlayerData{{ID: PlayerID("red"), Team: TeamRed}})

		snapshot := state.Step([]InputCommand{{
			PlayerID: PlayerID("red"), ClientTick: 1, PressedAttack: true,
		}})

		if snapshot.Players[0].PressedAttack || len(snapshot.Projectiles) != 0 {
			t.Fatalf("zero-direction attack had a visible effect: player=%+v projectiles=%+v", snapshot.Players[0], snapshot.Projectiles)
		}
		if got := snapshot.Players[0].LastProcessedClientTick; got != 1 {
			t.Fatalf("zero-direction ACK=%d want=1", got)
		}
	})

	t.Run("exhausted attack charge", func(t *testing.T) {
		state := NewState([]PlayerData{{ID: PlayerID("red"), Team: TeamRed}})
		maxCharges := StaticGameConfig().DefaultPlayerType().NormalAttack.MaxCharges
		for tick := int64(1); tick <= int64(maxCharges); tick++ {
			state.Step([]InputCommand{{
				PlayerID: PlayerID("red"), ClientTick: tick,
				AttackDir: Vector2{X: 1}, PressedAttack: true,
			}})
		}

		snapshot := state.Step([]InputCommand{{
			PlayerID: PlayerID("red"), ClientTick: int64(maxCharges + 1),
			AttackDir: Vector2{X: 1}, PressedAttack: true,
		}})

		if snapshot.Players[0].PressedAttack || len(snapshot.Projectiles) != maxCharges*5 {
			t.Fatalf("exhausted attack had a visible effect: player=%+v projectiles=%d", snapshot.Players[0], len(snapshot.Projectiles))
		}
		if got := snapshot.Players[0].LastProcessedClientTick; got != int64(maxCharges+1) {
			t.Fatalf("exhausted-attack ACK=%d want=%d", got, maxCharges+1)
		}
	})
}

func TestStepRejectsStaleAndDuplicateClientTick(t *testing.T) {
	for _, tc := range []struct {
		name       string
		clientTick int64
	}{
		{name: "stale", clientTick: 11},
		{name: "duplicate", clientTick: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := NewState([]PlayerData{{ID: PlayerID("red"), Team: TeamRed}})
			before := state.Step([]InputCommand{{
				PlayerID: PlayerID("red"), ClientTick: 12, MoveDir: Vector2{X: 1},
			}})

			after := state.Step([]InputCommand{{
				PlayerID: PlayerID("red"), ClientTick: tc.clientTick,
				MoveDir: Vector2{Y: 1}, AttackDir: Vector2{Y: 1}, PressedAttack: true,
			}})

			if !reflect.DeepEqual(after.Players, before.Players) {
				t.Fatalf("player state changed for %s tick: before=%+v after=%+v", tc.name, before.Players, after.Players)
			}
			if !reflect.DeepEqual(after.Projectiles, before.Projectiles) {
				t.Fatalf("projectiles changed for %s tick: before=%+v after=%+v", tc.name, before.Projectiles, after.Projectiles)
			}
		})
	}
}

func TestStepDoesNotAcknowledgeUnprocessedInput(t *testing.T) {
	tests := []struct {
		name    string
		player  PlayerData
		command InputCommand
	}{
		{
			name:    "negative tick",
			player:  PlayerData{ID: PlayerID("red"), Team: TeamRed, LastProcessedClientTick: 7},
			command: InputCommand{PlayerID: PlayerID("red"), ClientTick: -1, MoveDir: Vector2{X: 1}},
		},
		{
			name:    "unknown player",
			player:  PlayerData{ID: PlayerID("red"), Team: TeamRed, LastProcessedClientTick: 7},
			command: InputCommand{PlayerID: PlayerID("ghost"), ClientTick: 8, MoveDir: Vector2{X: 1}},
		},
		{
			name:    "dead player",
			player:  PlayerData{ID: PlayerID("red"), Team: TeamRed, HP: 100, IsDead: true, LastProcessedClientTick: 7},
			command: InputCommand{PlayerID: PlayerID("red"), ClientTick: 8, MoveDir: Vector2{X: 1}},
		},
		{
			name:    "non-finite movement",
			player:  PlayerData{ID: PlayerID("red"), Team: TeamRed, LastProcessedClientTick: 7},
			command: InputCommand{PlayerID: PlayerID("red"), ClientTick: 8, MoveDir: Vector2{X: math.NaN()}},
		},
		{
			name:    "non-finite attack",
			player:  PlayerData{ID: PlayerID("red"), Team: TeamRed, LastProcessedClientTick: 7},
			command: InputCommand{PlayerID: PlayerID("red"), ClientTick: 8, AttackDir: Vector2{Y: math.Inf(1)}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewState([]PlayerData{tc.player})
			before := state.Step(nil)

			after := state.Step([]InputCommand{tc.command})

			if !reflect.DeepEqual(after.Players, before.Players) {
				t.Fatalf("player state changed for unprocessed input: before=%+v after=%+v", before.Players, after.Players)
			}
			if !reflect.DeepEqual(after.Projectiles, before.Projectiles) {
				t.Fatalf("projectiles changed for unprocessed input: before=%+v after=%+v", before.Projectiles, after.Projectiles)
			}
		})
	}
}

func TestStepAppliesLegacyInputWithoutChangingACK(t *testing.T) {
	state := NewState([]PlayerData{{
		ID: PlayerID("red"), Team: TeamRed, LastProcessedClientTick: 12,
	}})

	snapshot := state.Step([]InputCommand{{
		PlayerID: PlayerID("red"), ClientTick: 0, MoveDir: Vector2{X: 1},
	}})

	assertVector(t, "legacy input position", snapshot.Players[0].Pos, Vector2{X: DefaultPlayerSpeed * TickDuration})
	if got := snapshot.Players[0].LastProcessedClientTick; got != 12 {
		t.Fatalf("legacy input ACK=%d want=12", got)
	}
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

func TestLoadMapDataRejectsMaxPlayersAboveUniqueSpawnCapacity(t *testing.T) {
	_, err := LoadMapData(strings.NewReader(`{
		"width": 4,
		"height": 4,
		"index": 0,
		"maxPlayers": 2,
		"tileSize": 1.2,
		"map": [
			[1, 1, 1, 1],
			[1, 2, 1, 1],
			[1, 4, 1, 1],
			[1, 1, 1, 1]
		]
	}`))
	if err == nil {
		t.Fatal("expected insufficient unique spawn capacity to be rejected")
	}
	if got, want := err.Error(), "map maxPlayers 2 exceeds unique spawn capacity 1"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestLoadMapDataAcceptsMaxPlayersAtUniqueSpawnCapacity(t *testing.T) {
	gameMap, err := LoadMapData(strings.NewReader(`{
		"width": 4,
		"height": 4,
		"index": 0,
		"maxPlayers": 2,
		"tileSize": 1.2,
		"map": [
			[1, 1, 1, 1],
			[1, 2, 0, 1],
			[1, 4, 1, 1],
			[1, 1, 1, 1]
		]
	}`))
	if err != nil {
		t.Fatalf("load map at exact spawn capacity: %v", err)
	}
	if gameMap.MaxPlayers != 2 {
		t.Fatalf("expected maxPlayers 2, got %d", gameMap.MaxPlayers)
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

func TestNewStateWithConfigUsesCharacterTypeStats(t *testing.T) {
	players := []PlayerData{
		{ID: "shelly", CharacterType: CharacterTypeShelly},
		{ID: "colt", CharacterType: CharacterTypeColt},
		{ID: "lily", CharacterType: CharacterTypeLily},
	}
	snapshot := NewStateWithConfig(players, Config{Game: StaticGameConfig()}).Step(nil)
	want := map[PlayerID]struct {
		characterType CharacterType
		hp            float64
	}{
		"shelly": {CharacterTypeShelly, 4000},
		"colt":   {CharacterTypeColt, 3100},
		"lily":   {CharacterTypeLily, 4100},
	}
	for _, player := range snapshot.Players {
		expected := want[player.ID]
		if player.CharacterType != expected.characterType || player.HP != expected.hp || player.Speed != 2 || player.Radius != 0.5 {
			t.Fatalf("player %q = %+v, want type=%d hp=%v speed=2 radius=0.5", player.ID, player, expected.characterType, expected.hp)
		}
	}
}

func TestNewStateWithConfigKeepsMixedCharacterStatsIndependent(t *testing.T) {
	state := NewStateWithConfig([]PlayerData{
		{ID: "colt", CharacterType: CharacterTypeColt},
		{ID: "lily", CharacterType: CharacterTypeLily},
	}, Config{Game: StaticGameConfig()})
	first := state.Step(nil)
	first.Players[0].HP = 1
	second := state.Step(nil)
	assertPlayerHP(t, second, "colt", 3100, false)
	assertPlayerHP(t, second, "lily", 4100, false)
}

func TestNewStateWithConfigPreservesPositiveCharacterStatOverrides(t *testing.T) {
	snapshot := NewStateWithConfig([]PlayerData{{
		ID:            "fixture",
		CharacterType: CharacterTypeLily,
		HP:            77,
		Speed:         3,
		Radius:        0.25,
	}}, Config{Game: StaticGameConfig()}).Step(nil)
	got := snapshot.Players[0]
	if got.HP != 77 || got.Speed != 3 || got.Radius != 0.25 || got.CharacterType != CharacterTypeLily {
		t.Fatalf("positive fixture override changed: %+v", got)
	}
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

func TestStepAppliesPlayerTileCollisionPolicy(t *testing.T) {
	tests := []struct {
		name    string
		tile    TileType
		blocked bool
	}{
		{name: "wall blocks", tile: TileWall, blocked: true},
		{name: "bush passes", tile: TileBush, blocked: false},
		{name: "water blocks", tile: TileWater, blocked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gameMap := collisionPolicyMap(tt.tile)
			center := gameMap.WorldPos(2, 2)
			step := DefaultPlayerSpeed * TickDuration
			start := Vector2{X: center.X - TileSize/2 - DefaultPlayerRadius - step + 0.001, Y: center.Y}
			state := NewStateWithConfig([]PlayerData{{
				ID: PlayerID("red-1"), Team: TeamRed, Slot: 0, Pos: start,
			}}, Config{Map: gameMap})

			snapshot := state.Step([]InputCommand{{
				PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: 1},
			}})
			want := Vector2{X: start.X + step, Y: start.Y}
			if tt.blocked {
				want = start
			}
			assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, want)
		})
	}
}

func TestStepKeepsPlayerInsideMapBoundary(t *testing.T) {
	gameMap := MapData{
		Width: 4, Height: 4, MaxPlayers: 6, TileSize: TileSize,
		Map: [][]TileType{
			{TileSpawnPoint, TileGround, TileGround, TileGround},
			{TileGround, TileGround, TileGround, TileGround},
			{TileGround, TileGround, TileGround, TileGround},
			{TileGround, TileGround, TileGround, TileSpawnPoint},
		},
	}
	step := DefaultPlayerSpeed * TickDuration
	mapMinX := gameMap.WorldPos(0, 0).X - TileSize/2
	start := Vector2{X: mapMinX + DefaultPlayerRadius + step - 0.001, Y: gameMap.WorldPos(0, 1).Y}
	state := NewStateWithConfig([]PlayerData{{
		ID: PlayerID("red-1"), Team: TeamRed, Slot: 0, Pos: start,
	}}, Config{Map: gameMap})
	if state.gameMap.Width != gameMap.Width || state.gameMap.Height != gameMap.Height {
		t.Fatalf("expected 4x4 boundary test map, got %dx%d", state.gameMap.Width, state.gameMap.Height)
	}

	snapshot := state.Step([]InputCommand{{
		PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: -1},
	}})

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
	if len(snapshot.Projectiles) != 5 {
		t.Fatalf("expected extreme finite attack direction to create 5 projectiles, got %d", len(snapshot.Projectiles))
	}
	assertVector(t, "extreme finite center projectile direction", snapshot.Projectiles[2].Dir, wantDirection)
}

func TestStepDeadPlayerInputIsIgnoredAfterProjectileHit(t *testing.T) {
	shooterPosition := Vector2{}
	targetPosition := Vector2{X: DefaultProjectileSpeed * TickDuration}
	state := newSingleProjectileTestState([]PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed, Pos: shooterPosition},
		{ID: PlayerID("blue-1"), Team: TeamBlue, Pos: targetPosition, HP: defaultShellyProjectileDamage()},
	}, Config{})

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
			ID:            PlayerID("red-1"),
			Team:          TeamRed,
			Slot:          0,
			Pos:           start,
			CharacterType: CharacterTypeShelly,
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

	if len(snapshot.Projectiles) != 5 {
		t.Fatalf("expected 5 Shelly projectiles, got %d", len(snapshot.Projectiles))
	}
	projectile := snapshot.Projectiles[2]
	if projectile.ID == "" {
		t.Fatal("expected projectile ID to be set")
	}
	if projectile.OwnerID != PlayerID("red-1") {
		t.Fatalf("expected projectile owner %q, got %q", PlayerID("red-1"), projectile.OwnerID)
	}
	assertVector(t, "projectile position", projectile.Pos, start)
	assertVector(t, "projectile direction", projectile.Dir, Vector2{X: 1, Y: 0})
	playerType, ok := StaticGameConfig().PlayerType(CharacterTypeShelly)
	if !ok {
		t.Fatal("missing Shelly player type")
	}
	projectileType, ok := StaticGameConfig().ProjectileType(playerType.NormalAttack.Projectile.Type)
	if !ok {
		t.Fatal("missing Shelly projectile type")
	}
	if projectile.Speed != projectileType.Speed {
		t.Fatalf("expected projectile speed %f, got %f", projectileType.Speed, projectile.Speed)
	}
	if projectile.Damage != 280 {
		t.Fatalf("expected Shelly projectile damage 280, got %f", projectile.Damage)
	}
	if projectile.Radius != projectileType.Radius {
		t.Fatalf("expected projectile radius %f, got %f", projectileType.Radius, projectile.Radius)
	}
	if projectile.IsDestroyed {
		t.Fatal("expected new projectile to start not destroyed")
	}
}

func TestStepLilyApprovalCreatesNoProjectile(t *testing.T) {
	state := NewState([]PlayerData{{
		ID:            PlayerID("lily-1"),
		Team:          TeamRed,
		CharacterType: CharacterTypeLily,
	}})

	snapshot := state.Step([]InputCommand{{
		PlayerID:      PlayerID("lily-1"),
		AttackDir:     Vector2{X: 1},
		PressedAttack: true,
	}})

	if !snapshot.Players[0].PressedAttack {
		t.Fatal("expected Lily attack approval to be recorded")
	}
	if len(snapshot.Projectiles) != 0 {
		t.Fatalf("expected Lily approval to create no projectile, got %+v", snapshot.Projectiles)
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
	for _, characterType := range []CharacterType{CharacterTypeShelly, CharacterTypeColt, CharacterTypeLily} {
		t.Run(fmt.Sprintf("%d", characterType), func(t *testing.T) {
			gameConfig := StaticGameConfig()
			gameConfig.Map = MapData{}
			for index := range gameConfig.Player.Types {
				if gameConfig.Player.Types[index].CharacterType == characterType {
					gameConfig.Player.Types[index].NormalAttack.RechargeTicks = 1000
				}
			}
			state := NewStateWithConfig([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed, CharacterType: characterType}}, Config{Game: gameConfig})
			playerType, ok := gameConfig.PlayerType(characterType)
			if !ok {
				t.Fatalf("missing player type %d", characterType)
			}

			for attack := 0; attack < playerType.NormalAttack.MaxCharges; attack++ {
				snapshot := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
				if !snapshot.Players[0].PressedAttack {
					t.Fatalf("expected attack %d to be accepted", attack+1)
				}
				if characterType == CharacterTypeColt {
					for range 30 {
						state.Step(nil)
					}
				}
			}
			exhausted := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})

			wantProjectiles := 0
			if playerType.NormalAttack.Projectile != nil {
				wantProjectiles = playerType.NormalAttack.MaxCharges * playerType.NormalAttack.Projectile.Count
			}
			if got := len(exhausted.Projectiles); got != wantProjectiles {
				t.Fatalf("expected exhausted attack to leave %d projectiles, got %d", wantProjectiles, got)
			}
			if exhausted.Players[0].PressedAttack {
				t.Fatal("expected exhausted fifth attack to leave PressedAttack false")
			}
		})
	}
}

func TestStepRestoresAttackChargeAfterRechargeTicks(t *testing.T) {
	for _, characterType := range []CharacterType{CharacterTypeShelly, CharacterTypeColt, CharacterTypeLily} {
		t.Run(fmt.Sprintf("%d", characterType), func(t *testing.T) {
			gameConfig := StaticGameConfig()
			for index := range gameConfig.Player.Types {
				gameConfig.Player.Types[index].NormalAttack.MaxCharges = 1
			}
			gameConfig.Map = MapData{}
			state := NewStateWithConfig([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed, CharacterType: characterType}}, Config{Game: gameConfig})

			state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
			for tick := 0; tick < 28; tick++ {
				state.Step(nil)
			}
			notYetRecharged := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
			playerType, ok := gameConfig.PlayerType(characterType)
			if !ok {
				t.Fatalf("missing player type %d", characterType)
			}
			wantProjectiles := 0
			if playerType.NormalAttack.Projectile != nil {
				switch characterType {
				case CharacterTypeShelly:
					wantProjectiles = 5
				case CharacterTypeColt:
					wantProjectiles = 5
				}
			}
			if got := len(notYetRecharged.Projectiles); got != wantProjectiles {
				t.Fatalf("expected no recharge before 30 ticks, got %d projectiles", got)
			}
			if notYetRecharged.Players[0].PressedAttack {
				t.Fatal("expected attack before recharge completion to be ignored")
			}

			recharged := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
			if characterType == CharacterTypeColt {
				if recharged.Players[0].PressedAttack {
					t.Fatal("expected reattack on the last burst emission tick to be rejected")
				}
				if got := len(recharged.Projectiles); got != 6 {
					t.Fatalf("expected Colt's last scheduled emission, got %d projectiles", got)
				}
				recharged = state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
				if got := len(recharged.Projectiles); got != 7 {
					t.Fatalf("expected Colt reactivation on the next tick, got %d projectiles", got)
				}
			} else if got := len(recharged.Projectiles); got != 2*wantProjectiles {
				t.Fatalf("expected one restored charge after 30 ticks, got %d projectiles", got)
			}
			if !recharged.Players[0].PressedAttack {
				t.Fatal("expected attack after recharge and non-overlap completion to be accepted")
			}
		})
	}
}

func TestStepAttackChargeDoesNotAccumulateAboveMaximum(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})
	maxCharges := StaticGameConfig().DefaultPlayerType().NormalAttack.MaxCharges
	for tick := 0; tick < 120; tick++ {
		state.Step(nil)
	}

	for attack := 0; attack < maxCharges+2; attack++ {
		state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
	}
	snapshot := state.Step(nil)

	if got := len(snapshot.Projectiles); got != maxCharges*5 {
		t.Fatalf("expected charge capacity to remain capped at %d attacks, got %d projectiles", maxCharges, got)
	}
}

func TestStepZeroDirectionDoesNotConsumeAttackCharge(t *testing.T) {
	state := NewState([]PlayerData{{ID: PlayerID("red-1"), Team: TeamRed}})
	maxCharges := StaticGameConfig().DefaultPlayerType().NormalAttack.MaxCharges

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

	for attack := 0; attack < maxCharges; attack++ {
		state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
	}
	exhausted := state.Step([]InputCommand{attackInput(PlayerID("red-1"))})
	if got := len(exhausted.Projectiles); got != maxCharges*5 {
		t.Fatalf("expected zero direction to consume no charge, got %d projectiles", got)
	}
}

func TestStepKeepsAttackChargesSeparatePerPlayer(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: PlayerID("red-1"), Team: TeamRed},
		{ID: PlayerID("blue-1"), Team: TeamBlue},
	})

	maxCharges := StaticGameConfig().DefaultPlayerType().NormalAttack.MaxCharges
	for attack := 0; attack < maxCharges; attack++ {
		state.Step([]InputCommand{
			attackInput(PlayerID("red-1")),
			attackInput(PlayerID("blue-1")),
		})
	}
	exhausted := state.Step([]InputCommand{
		attackInput(PlayerID("red-1")),
		attackInput(PlayerID("blue-1")),
	})

	if got := len(exhausted.Projectiles); got != 2*maxCharges*5 {
		t.Fatalf("expected two independent %d-charge budgets, got %d projectiles", maxCharges, got)
	}
	if exhausted.Players[0].PressedAttack || exhausted.Players[1].PressedAttack {
		t.Fatal("expected both exhausted attacks to leave PressedAttack false")
	}
}

func TestStepProcessesMovementAndAttackInSameTick(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	state := newSingleProjectileTestState([]PlayerData{
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
	state := newSingleProjectileTestState([]PlayerData{
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
	state := newSingleProjectileTestState([]PlayerData{
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
	state := newSingleProjectileTestState([]PlayerData{
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

func TestStepAppliesProjectileTileCollisionPolicy(t *testing.T) {
	tests := []struct {
		name      string
		tile      TileType
		destroyed bool
	}{
		{name: "wall destroys", tile: TileWall, destroyed: true},
		{name: "bush passes", tile: TileBush, destroyed: false},
		{name: "water passes", tile: TileWater, destroyed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gameMap := collisionPolicyMap(tt.tile)
			center := gameMap.WorldPos(2, 2)
			step := DefaultProjectileSpeed * TickDuration
			start := Vector2{X: center.X - TileSize/2 - DefaultProjectileRadius - step + 0.001, Y: center.Y}
			state := newSingleProjectileTestState([]PlayerData{{
				ID: PlayerID("red-1"), Team: TeamRed, Slot: 0, Pos: start,
			}}, Config{Map: gameMap})

			state.Step([]InputCommand{{
				PlayerID: PlayerID("red-1"), AttackDir: Vector2{X: 1}, PressedAttack: true,
			}})
			snapshot := state.Step(nil)
			if len(snapshot.Projectiles) != 1 {
				t.Fatalf("expected one projectile, got %d", len(snapshot.Projectiles))
			}
			if snapshot.Projectiles[0].IsDestroyed != tt.destroyed {
				t.Fatalf("expected destroyed=%t on tile %d, got %+v", tt.destroyed, tt.tile, snapshot.Projectiles[0])
			}
		})
	}
}

func collisionPolicyMap(center TileType) MapData {
	gameMap := StaticMapFixture()
	gameMap.Map[2][2] = center
	return gameMap
}

func TestStepProjectileHitReducesTargetHPAndDestroysProjectile(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	target := Vector2{
		X: start.X + DefaultProjectileSpeed*TickDuration,
		Y: start.Y,
	}
	state := newSingleProjectileTestState([]PlayerData{
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
	assertPlayerHP(t, snapshot, PlayerID("blue-1"), DefaultPlayerHP-defaultShellyProjectileDamage(), false)
	if len(snapshot.Projectiles) != 1 {
		t.Fatalf("expected 1 projectile, got %d", len(snapshot.Projectiles))
	}
	if !snapshot.Projectiles[0].IsDestroyed {
		t.Fatal("expected projectile to be destroyed after hitting player")
	}

	next := state.Step(nil)

	assertPlayerHP(t, next, PlayerID("blue-1"), DefaultPlayerHP-defaultShellyProjectileDamage(), false)
	if !next.Projectiles[0].IsDestroyed {
		t.Fatal("expected hit projectile to stay destroyed")
	}
}

func TestStepProjectileDoesNotSelfHitOwner(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	state := newSingleProjectileTestState([]PlayerData{
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
	state := newSingleProjectileTestState([]PlayerData{
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
			HP:   defaultShellyProjectileDamage(),
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

func TestStepProjectileCollisionMatrix(t *testing.T) {
	start := StaticMapFixture().WorldPos(1, 1)
	overlap := Vector2{
		X: start.X + DefaultProjectileSpeed*TickDuration,
		Y: start.Y,
	}
	type expectedPlayer struct {
		id     PlayerID
		hp     float64
		isDead bool
	}
	tests := []struct {
		name      string
		mode      string
		players   []PlayerData
		expected  []expectedPlayer
		destroyed bool
	}{
		{
			name: "solo owner",
			mode: GameModeSolo,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: Team("solo-1"), Pos: start},
			},
			expected:  []expectedPlayer{{id: PlayerID("owner"), hp: DefaultPlayerHP}},
			destroyed: false,
		},
		{
			name: "solo dead player",
			mode: GameModeSolo,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: Team("solo-1"), Pos: start},
				{ID: PlayerID("target"), Team: Team("solo-2"), Pos: overlap, HP: 60, IsDead: true},
			},
			expected:  []expectedPlayer{{id: PlayerID("target"), hp: 60, isDead: true}},
			destroyed: false,
		},
		{
			name: "solo same team label live non-owner",
			mode: GameModeSolo,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: Team("shared"), Pos: start},
				{ID: PlayerID("target"), Team: Team("shared"), Pos: overlap},
			},
			expected:  []expectedPlayer{{id: PlayerID("target"), hp: DefaultPlayerHP - defaultShellyProjectileDamage()}},
			destroyed: true,
		},
		{
			name: "team ally only",
			mode: GameModeTeam,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: TeamRed, Pos: start},
				{ID: PlayerID("ally"), Team: TeamRed, Pos: overlap},
			},
			expected:  []expectedPlayer{{id: PlayerID("ally"), hp: DefaultPlayerHP}},
			destroyed: false,
		},
		{
			name: "team dead enemy",
			mode: GameModeTeam,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: TeamRed, Pos: start},
				{ID: PlayerID("enemy"), Team: TeamBlue, Pos: overlap, HP: 60, IsDead: true},
			},
			expected:  []expectedPlayer{{id: PlayerID("enemy"), hp: 60, isDead: true}},
			destroyed: false,
		},
		{
			name: "team enemy",
			mode: GameModeTeam,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: TeamRed, Pos: start},
				{ID: PlayerID("enemy"), Team: TeamBlue, Pos: overlap},
			},
			expected:  []expectedPlayer{{id: PlayerID("enemy"), hp: DefaultPlayerHP - defaultShellyProjectileDamage()}},
			destroyed: true,
		},
		{
			name: "duel owner",
			mode: GameModeDuel1v1,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: TeamRed, Pos: start},
			},
			expected:  []expectedPlayer{{id: PlayerID("owner"), hp: DefaultPlayerHP}},
			destroyed: false,
		},
		{
			name: "duel dead opponent",
			mode: GameModeDuel1v1,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: TeamRed, Pos: start},
				{ID: PlayerID("opponent"), Team: TeamBlue, Pos: overlap, HP: 60, IsDead: true},
			},
			expected:  []expectedPlayer{{id: PlayerID("opponent"), hp: 60, isDead: true}},
			destroyed: false,
		},
		{
			name: "duel live opponent",
			mode: GameModeDuel1v1,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: TeamRed, Pos: start},
				{ID: PlayerID("opponent"), Team: TeamBlue, Pos: overlap},
			},
			expected:  []expectedPlayer{{id: PlayerID("opponent"), hp: DefaultPlayerHP - defaultShellyProjectileDamage()}},
			destroyed: true,
		},
		{
			name: "first eligible overlap preserves player order",
			mode: GameModeSolo,
			players: []PlayerData{
				{ID: PlayerID("owner"), Team: Team("solo-1"), Pos: start},
				{ID: PlayerID("target-z"), Team: Team("solo-2"), Pos: overlap},
				{ID: PlayerID("target-a"), Team: Team("solo-3"), Pos: overlap},
			},
			expected: []expectedPlayer{
				{id: PlayerID("target-z"), hp: DefaultPlayerHP - defaultShellyProjectileDamage()},
				{id: PlayerID("target-a"), hp: DefaultPlayerHP},
			},
			destroyed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gameConfig, err := StaticGameConfig().SelectMode(tt.mode)
			if err != nil {
				t.Fatalf("select mode %q: %v", tt.mode, err)
			}
			state := newSingleProjectileTestState(tt.players, Config{Map: StaticMapFixture(), Game: gameConfig})

			state.Step([]InputCommand{attackInput(PlayerID("owner"))})
			snapshot := state.Step(nil)

			for _, player := range tt.expected {
				assertPlayerHP(t, snapshot, player.id, player.hp, player.isDead)
			}
			if len(snapshot.Projectiles) != 1 {
				t.Fatalf("expected one projectile, got %d", len(snapshot.Projectiles))
			}
			if snapshot.Projectiles[0].IsDestroyed != tt.destroyed {
				t.Fatalf("expected projectile destroyed=%t, got %+v", tt.destroyed, snapshot.Projectiles[0])
			}
		})
	}
}

func TestStepNormalizesInputOrderThroughCollision(t *testing.T) {
	step := DefaultProjectileSpeed * TickDuration
	playerAStart := StaticMapFixture().WorldPos(1, 1)
	playerBStart := Vector2{X: playerAStart.X + step, Y: playerAStart.Y}
	players := []PlayerData{
		{ID: PlayerID("player-a"), Team: TeamRed, Pos: playerAStart},
		{ID: PlayerID("player-b"), Team: TeamBlue, Pos: playerBStart},
	}
	inputs := []InputCommand{
		{PlayerID: PlayerID("player-b"), AttackDir: Vector2{X: -1}, PressedAttack: true},
		{PlayerID: PlayerID("player-a"), AttackDir: Vector2{X: 1}, PressedAttack: true},
	}
	reversedInputs := []InputCommand{inputs[1], inputs[0]}
	wantInputs := append([]InputCommand(nil), inputs...)
	wantReversedInputs := append([]InputCommand(nil), reversedInputs...)

	state := newSingleProjectileTestState(players, Config{Map: StaticMapFixture()})
	reversedState := newSingleProjectileTestState(players, Config{Map: StaticMapFixture()})
	created := state.Step(inputs)
	reversedCreated := reversedState.Step(reversedInputs)
	collided := state.Step(nil)
	reversedCollided := reversedState.Step(nil)

	if !reflect.DeepEqual(inputs, wantInputs) {
		t.Errorf("Step mutated caller inputs:\n got: %+v\nwant: %+v", inputs, wantInputs)
	}
	if !reflect.DeepEqual(reversedInputs, wantReversedInputs) {
		t.Errorf("Step mutated reversed caller inputs:\n got: %+v\nwant: %+v", reversedInputs, wantReversedInputs)
	}
	if !reflect.DeepEqual(created, reversedCreated) {
		t.Errorf("creation snapshots differ by input order:\n first: %+v\nsecond: %+v", created, reversedCreated)
	}
	if !reflect.DeepEqual(collided, reversedCollided) {
		t.Errorf("collision snapshots differ by input order:\n first: %+v\nsecond: %+v", collided, reversedCollided)
	}

	wantProjectileIDs := []ProjectileID{
		ProjectileID("projectile-1-player-a-1"),
		ProjectileID("projectile-1-player-b-2"),
	}
	for _, result := range []struct {
		label    string
		snapshot Snapshot
	}{
		{label: "first", snapshot: collided},
		{label: "second", snapshot: reversedCollided},
	} {
		label := result.label
		snapshot := result.snapshot
		assertPlayerHP(t, snapshot, PlayerID("player-a"), DefaultPlayerHP-defaultShellyProjectileDamage(), false)
		assertPlayerHP(t, snapshot, PlayerID("player-b"), DefaultPlayerHP-defaultShellyProjectileDamage(), false)
		if len(snapshot.Projectiles) != len(wantProjectileIDs) {
			t.Fatalf("%s collision snapshot: expected %d projectiles, got %d", label, len(wantProjectileIDs), len(snapshot.Projectiles))
		}
		for i, wantID := range wantProjectileIDs {
			if snapshot.Projectiles[i].ID != wantID {
				t.Errorf("%s collision snapshot: expected projectile %d ID %q, got %q", label, i, wantID, snapshot.Projectiles[i].ID)
			}
			if !snapshot.Projectiles[i].IsDestroyed {
				t.Errorf("%s collision snapshot: expected projectile %q to be destroyed", label, snapshot.Projectiles[i].ID)
			}
		}
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

func playerByID(t *testing.T, snapshot Snapshot, id PlayerID) PlayerData {
	t.Helper()

	for _, player := range snapshot.Players {
		if player.ID == id {
			return player
		}
	}

	t.Fatalf("expected snapshot to include player %s", id)
	return PlayerData{}
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
