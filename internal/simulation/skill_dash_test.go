package simulation

import (
	"math"
	"reflect"
	"testing"
)

const dashTestTolerance = 1e-9

func TestShellySkillReloadsAndDashesExactConfiguredDistance(t *testing.T) {
	tests := []struct {
		name      string
		direction Vector2
		want      Vector2
	}{
		{name: "axis", direction: Vector2{X: 1}, want: Vector2{X: 6.48}},
		{name: "diagonal", direction: Vector2{X: 1, Y: 1}, want: Vector2{X: 6.48 / math.Sqrt2, Y: 6.48 / math.Sqrt2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newShellyDashTestState(t, []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly}}, MapData{})
			state.attackStates["shelly"] = attackState{charges: 0, rechargeTicks: 17}

			snapshot := state.Step([]InputCommand{{
				PlayerID: "shelly", ClientTick: 1, AttackDir: tt.direction, PressedSkill: true,
			}})
			player := playerByID(t, snapshot, "shelly")

			assertVectorClose(t, "dash position", player.Pos, tt.want, dashTestTolerance)
			if !player.PressedSkill || player.SkillReadyTick != 361 {
				t.Fatalf("skill state = pressed:%t ready:%d, want true/361", player.PressedSkill, player.SkillReadyTick)
			}
			if player.AttackCharges != 3 || player.NextAttackChargeTick != 0 {
				t.Fatalf("snapshot attack state = %d/%d, want 3/0", player.AttackCharges, player.NextAttackChargeTick)
			}
			if got := state.attackStates["shelly"]; got != (attackState{charges: 3}) {
				t.Fatalf("private attack state = %+v, want full charge and reset recharge", got)
			}
		})
	}
}

func TestShellyDashStartsAfterNormalMovement(t *testing.T) {
	state := newShellyDashTestState(t, []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly}}, MapData{})

	snapshot := state.Step([]InputCommand{{
		PlayerID: "shelly", ClientTick: 1,
		MoveDir: Vector2{X: 1}, AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})

	want := Vector2{X: DefaultPlayerSpeed*TickDuration + 6.48}
	assertVectorClose(t, "post-movement dash position", playerByID(t, snapshot, "shelly").Pos, want, dashTestTolerance)
}

func TestShellySkillReloadsEveryChargeBoundary(t *testing.T) {
	for _, initial := range []attackState{
		{charges: 0, rechargeTicks: 0},
		{charges: 1, rechargeTicks: 29},
		{charges: 2, rechargeTicks: 7},
		{charges: 3, rechargeTicks: 0},
	} {
		name := string(rune('0' + initial.charges))
		t.Run(name, func(t *testing.T) {
			state := newShellyDashTestState(t, []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly}}, MapData{})
			state.attackStates["shelly"] = initial

			snapshot := state.Step([]InputCommand{{
				PlayerID: "shelly", AttackDir: Vector2{X: 1}, PressedSkill: true,
			}})

			if got := state.attackStates["shelly"]; got != (attackState{charges: 3}) {
				t.Fatalf("initial=%+v final=%+v, want full charge and reset recharge", initial, got)
			}
			player := playerByID(t, snapshot, "shelly")
			if player.AttackCharges != 3 || player.NextAttackChargeTick != 0 {
				t.Fatalf("snapshot attack state = %d/%d, want 3/0", player.AttackCharges, player.NextAttackChargeTick)
			}
		})
	}
}

func TestShellyDashStopsBeforeMapAndPlayerContact(t *testing.T) {
	tests := []struct {
		name       string
		gameMap    MapData
		players    []PlayerData
		wantX      float64
		wantReload bool
	}{
		{
			name:    "wall",
			gameMap: dashLineMap(TileWall),
			players: []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -4}}},
			wantX:   -1.100001, wantReload: true,
		},
		{
			name:    "water",
			gameMap: dashLineMap(TileWater),
			players: []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -4}}},
			wantX:   -1.100001, wantReload: true,
		},
		{
			name:    "bush passes",
			gameMap: dashLineMap(TileBush),
			players: []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -4}}},
			wantX:   2.48, wantReload: true,
		},
		{
			name:    "spawn passes",
			gameMap: dashLineMap(TileSpawnPoint),
			players: []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -4}}},
			wantX:   2.48, wantReload: true,
		},
		{
			name:    "boundary",
			gameMap: dashOpenMap(),
			players: []PlayerData{{ID: "shelly", CharacterType: CharacterTypeShelly, Pos: Vector2{X: 8}}},
			wantX:   12.099999, wantReload: true,
		},
		{
			name:    "live player",
			gameMap: MapData{},
			players: []PlayerData{
				{ID: "shelly", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -4}},
				{ID: "blocker", CharacterType: CharacterTypeColt, Pos: Vector2{}},
			},
			wantX: -1.000001, wantReload: true,
		},
		{
			name:    "fully blocked by live player",
			gameMap: MapData{},
			players: []PlayerData{
				{ID: "shelly", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -1.000001}},
				{ID: "blocker", CharacterType: CharacterTypeColt, Pos: Vector2{}},
			},
			wantX: -1.000001, wantReload: true,
		},
		{
			name:    "dead player passes",
			gameMap: MapData{},
			players: []PlayerData{
				{ID: "shelly", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -4}},
				{ID: "blocker", CharacterType: CharacterTypeColt, Pos: Vector2{}, IsDead: true},
			},
			wantX: 2.48, wantReload: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newShellyDashTestState(t, tt.players, tt.gameMap)
			state.attackStates["shelly"] = attackState{charges: 1, rechargeTicks: 19}

			snapshot := state.Step([]InputCommand{{
				PlayerID: "shelly", ClientTick: 1, AttackDir: Vector2{X: 1}, PressedSkill: true,
			}})
			player := playerByID(t, snapshot, "shelly")
			if math.Abs(player.Pos.X-tt.wantX) > dashTestTolerance || math.Abs(player.Pos.Y) > dashTestTolerance {
				t.Fatalf("dash position = %+v, want x=%v", player.Pos, tt.wantX)
			}
			if !player.PressedSkill || player.SkillReadyTick != 361 || player.AttackCharges != 3 || player.NextAttackChargeTick != 0 {
				t.Fatalf("blocked dash refunded effect: %+v", player)
			}
		})
	}
}

func TestShellySimultaneousDashIsOrderIndependent(t *testing.T) {
	players := []PlayerData{
		{ID: "shelly-b", CharacterType: CharacterTypeShelly, Pos: Vector2{X: 4}},
		{ID: "shelly-a", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -4}},
	}
	inputs := []InputCommand{
		{PlayerID: "shelly-b", ClientTick: 1, MoveDir: Vector2{X: -1}, AttackDir: Vector2{X: -1}, PressedSkill: true},
		{PlayerID: "shelly-a", ClientTick: 1, MoveDir: Vector2{X: 1}, AttackDir: Vector2{X: 1}, PressedSkill: true},
	}

	forwardState := newShellyDashTestState(t, players, MapData{})
	reversedState := newShellyDashTestState(t, players, MapData{})
	forward := forwardState.Step(inputs)
	reversed := reversedState.Step([]InputCommand{inputs[1], inputs[0]})

	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("simultaneous dash differs by input order:\nforward=%+v\nreversed=%+v", forward, reversed)
	}
	assertVectorClose(t, "shelly-a contact", playerByID(t, forward, "shelly-a").Pos, Vector2{X: -0.500001}, dashTestTolerance)
	assertVectorClose(t, "shelly-b contact", playerByID(t, forward, "shelly-b").Pos, Vector2{X: 0.500001}, dashTestTolerance)
}

func TestShellyThreeWayDashStopsTransitiveContactIndependentOfPlayerSliceOrder(t *testing.T) {
	const radiusFromCenter = 2.0
	players := []PlayerData{
		{ID: "shelly-c", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -1, Y: -math.Sqrt(3)}},
		{ID: "shelly-a", CharacterType: CharacterTypeShelly, Pos: Vector2{X: radiusFromCenter}},
		{ID: "shelly-b", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -1, Y: math.Sqrt(3)}},
	}
	inputs := []InputCommand{
		{PlayerID: "shelly-c", AttackDir: Vector2{X: 1, Y: math.Sqrt(3)}, PressedSkill: true},
		{PlayerID: "shelly-a", AttackDir: Vector2{X: -1}, PressedSkill: true},
		{PlayerID: "shelly-b", AttackDir: Vector2{X: 1, Y: -math.Sqrt(3)}, PressedSkill: true},
	}
	reversedPlayers := []PlayerData{players[2], players[1], players[0]}
	reversedInputs := []InputCommand{inputs[2], inputs[1], inputs[0]}

	forward := newShellyDashTestState(t, players, MapData{}).Step(inputs)
	reversed := newShellyDashTestState(t, reversedPlayers, MapData{}).Step(reversedInputs)
	wantRadius := 1/math.Sqrt(3) + dashCollisionEpsilon
	for _, id := range []PlayerID{"shelly-a", "shelly-b", "shelly-c"} {
		gotForward := playerByID(t, forward, id).Pos
		gotReversed := playerByID(t, reversed, id).Pos
		assertVectorClose(t, string(id)+" slice-order position", gotForward, gotReversed, dashTestTolerance)
		if math.Abs(math.Hypot(gotForward.X, gotForward.Y)-wantRadius) > dashTestTolerance {
			t.Fatalf("%s radius = %v, want %v", id, math.Hypot(gotForward.X, gotForward.Y), wantRadius)
		}
	}
	for _, pair := range [][2]PlayerID{{"shelly-a", "shelly-b"}, {"shelly-b", "shelly-c"}, {"shelly-c", "shelly-a"}} {
		left := playerByID(t, forward, pair[0]).Pos
		right := playerByID(t, forward, pair[1]).Pos
		if distance := math.Hypot(left.X-right.X, left.Y-right.Y); math.Abs(distance-(1+math.Sqrt(3)*dashCollisionEpsilon)) > dashTestTolerance {
			t.Fatalf("pair %v distance = %v, want first-contact backoff", pair, distance)
		}
	}
}

func TestProjectileDeathBeforeInputRejectsShellyDash(t *testing.T) {
	state := newShellyDashTestState(t, []PlayerData{
		{ID: "owner", Team: TeamRed, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
		{ID: "shelly", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: DefaultProjectileSpeed * TickDuration}, HP: defaultShellyProjectileDamage()},
	}, MapData{})
	state.Step([]InputCommand{{
		PlayerID: "owner", AttackDir: Vector2{X: 1}, PressedAttack: true,
	}})

	snapshot := state.Step([]InputCommand{{
		PlayerID: "shelly", ClientTick: 1, AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})
	player := playerByID(t, snapshot, "shelly")

	if !player.IsDead || player.HP != 0 {
		t.Fatalf("projectile-before-input player = %+v, want dead", player)
	}
	if player.PressedSkill || player.SkillReadyTick != 0 || player.LastProcessedClientTick != 0 {
		t.Fatalf("dead Shelly input was approved or ACKed: %+v", player)
	}
	assertVectorClose(t, "dead Shelly position", player.Pos, Vector2{X: DefaultProjectileSpeed * TickDuration}, dashTestTolerance)
}

func TestShellyDashStoppedByWallBecomesBlocker(t *testing.T) {
	players := []PlayerData{
		{ID: "front", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -3}},
		{ID: "rear", CharacterType: CharacterTypeShelly, Pos: Vector2{X: -5}},
	}
	state := newShellyDashTestState(t, players, dashWallAtTwoMap())

	snapshot := state.Step([]InputCommand{
		{PlayerID: "front", AttackDir: Vector2{X: 1}, PressedSkill: true},
		{PlayerID: "rear", AttackDir: Vector2{X: 1}, PressedSkill: true},
	})

	front := playerByID(t, snapshot, "front")
	rear := playerByID(t, snapshot, "rear")
	assertVectorClose(t, "front wall stop", front.Pos, Vector2{X: 1.299999}, dashTestTolerance)
	assertVectorClose(t, "rear stopped-player contact", rear.Pos, Vector2{X: 0.299998}, dashTestTolerance)
}

func TestSweptCircleAABBUsesRoundedCornerAndFaceContact(t *testing.T) {
	boxMin := Vector2{X: -0.6, Y: -0.6}
	boxMax := Vector2{X: 0.6, Y: 0.6}
	tests := []struct {
		name       string
		start, end Vector2
		want       float64
	}{
		{name: "face", start: Vector2{X: -2}, end: Vector2{X: 2}, want: 0.225},
		{
			name:  "rounded corner",
			start: Vector2{X: -2, Y: -2}, end: Vector2{X: 2, Y: 2},
			want: (-0.6 - 0.5/math.Sqrt2 + 2) / 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, hit := sweptCircleAABBContactTime(tt.start, tt.end, 0.5, boxMin, boxMax)
			if !hit || math.Abs(got-tt.want) > dashTestTolerance {
				t.Fatalf("contact=(%v,%t), want (%v,true)", got, hit, tt.want)
			}
		})
	}
}

func TestMovingDashCirclesMaySeparateFromExistingContact(t *testing.T) {
	if contact, hit := movingCirclesContactTime(
		Vector2{X: -0.5}, Vector2{X: -2}, 0.5,
		Vector2{X: 0.5}, Vector2{X: 2}, 0.5,
	); hit {
		t.Fatalf("separating tangent circles reported contact at %v", contact)
	}
}

func newShellyDashTestState(t *testing.T, players []PlayerData, gameMap MapData) *State {
	t.Helper()
	config := skillTestGameConfig(t, CharacterTypeShelly, 360)
	config.Map = MapData{}
	state := NewStateWithConfig(players, Config{Game: config})
	state.gameMap = normalizeMap(gameMap)
	state.gameConfig.Map = state.gameMap
	return state
}

func dashOpenMap() MapData {
	const width, height = 21, 5
	rows := make([][]TileType, height)
	for y := range rows {
		rows[y] = make([]TileType, width)
	}
	return MapData{Width: width, Height: height, MaxPlayers: 6, TileSize: 1.2, Map: rows}
}

func dashLineMap(blocker TileType) MapData {
	gameMap := dashOpenMap()
	gameMap.Map[2][10] = blocker
	return gameMap
}

func dashWallAtTwoMap() MapData {
	gameMap := dashOpenMap()
	gameMap.Map[2][12] = TileWall
	return gameMap
}

func assertVectorClose(t *testing.T, label string, got, want Vector2, tolerance float64) {
	t.Helper()
	if math.Abs(got.X-want.X) > tolerance || math.Abs(got.Y-want.Y) > tolerance {
		t.Fatalf("%s = %+v, want %+v", label, got, want)
	}
}
