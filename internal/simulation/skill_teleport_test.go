package simulation

import (
	"math"
	"reflect"
	"testing"
)

func TestLilySkillApprovalEmitsCanonicalSeed(t *testing.T) {
	state := newLilySkillState([]PlayerData{{
		ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2},
	}}, MapData{})

	snapshot := state.Step([]InputCommand{{
		PlayerID: "lily", ClientTick: 1, AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})

	lily := playerByID(t, snapshot, "lily")
	if !lily.PressedSkill || lily.SkillReadyTick != 331 {
		t.Fatalf("Lily approval = %+v, want PressedSkill and ready tick 331", lily)
	}
	if len(snapshot.Projectiles) != 1 {
		t.Fatalf("projectiles = %+v, want one Lily seed", snapshot.Projectiles)
	}
	seed := snapshot.Projectiles[0]
	if seed.OwnerID != "lily" || seed.Type != "lily_seed" || seed.Damage != 400 || seed.Speed != 13 || seed.Radius != 0.3 {
		t.Fatalf("seed = %+v, want canonical Lily skill projectile", seed)
	}
	if runtime := state.projectileRuntime[seed.ID]; math.Abs(runtime.maxDistance-10.4*TileSize) > 1e-12 {
		t.Fatalf("seed max distance = %v, want %v", runtime.maxDistance, 10.4*TileSize)
	}
}

func TestLilySeedDealsDamageThenTeleportsFromPreHitTargetPosition(t *testing.T) {
	state := newLilySkillState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}, HP: 300},
	}, MapData{})

	snapshot := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)

	assertPlayerHP(t, snapshot, "target", 0, true)
	assertVectorNear(t, "lethal-hit Lily teleport", playerByID(t, snapshot, "lily").Pos, Vector2{X: TileSize}, 1e-9)
}

func TestLilySeedDeadOwnerKeepsDamageButSkipsTeleport(t *testing.T) {
	state := newLilySkillState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
	}, MapData{})
	state.Step([]InputCommand{{PlayerID: "lily", AttackDir: Vector2{X: 1}, PressedSkill: true}})
	state.EliminatePlayers([]PlayerID{"lily"})

	snapshot := resolveLilySeed(t, state, nil)

	assertPlayerHP(t, snapshot, "target", DefaultPlayerHP-400, false)
	lily := playerByID(t, snapshot, "lily")
	if !lily.IsDead || lily.Pos != (Vector2{X: -2}) {
		t.Fatalf("dead owner = %+v, want damage-only without teleport", lily)
	}
}

func TestLilyTeleportBacksOffToLargestValidPointOnSameRay(t *testing.T) {
	state := newLilySkillState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
		{ID: "blocker", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 2.15}},
	}, MapData{})

	snapshot := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)

	lilyPosition := playerByID(t, snapshot, "lily").Pos
	if lilyPosition.X <= 1.000001 || lilyPosition.X >= 1.2 || circlesOverlap(lilyPosition, DefaultPlayerRadius, state.players[2].Pos, state.players[2].Radius) {
		t.Fatalf("backoff destination=%+v, want largest safe point between minimum clearance and desired", lilyPosition)
	}
	nextPosition := Vector2{X: math.Nextafter(lilyPosition.X, math.Inf(1))}
	if !circlesOverlap(nextPosition, DefaultPlayerRadius, state.players[2].Pos, state.players[2].Radius) {
		t.Fatalf("backoff destination %+v is not the largest representable safe point", lilyPosition)
	}
	assertPlayerHP(t, snapshot, "target", DefaultPlayerHP-400, false)
	assertPlayerHP(t, snapshot, "blocker", DefaultPlayerHP, false)
}

func TestLilyTeleportPreservesNarrowValidBackoffGap(t *testing.T) {
	state := newLilySkillState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
		{ID: "blocker", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 2.0000015}},
	}, MapData{})

	snapshot := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)

	minimumClearance := 1.000001
	got := playerByID(t, snapshot, "lily").Pos.X
	position := Vector2{X: got}
	if got <= minimumClearance || circlesOverlap(position, DefaultPlayerRadius, state.players[2].Pos, state.players[2].Radius) {
		t.Fatalf("narrow-gap destination=%0.16f, want safe point above minimum %0.16f", got, minimumClearance)
	}
	nextPosition := Vector2{X: math.Nextafter(got, math.Inf(1))}
	if !circlesOverlap(nextPosition, DefaultPlayerRadius, state.players[2].Pos, state.players[2].Radius) {
		t.Fatalf("narrow-gap destination=%0.16f is not the largest representable safe point", got)
	}
}

func TestLilyTeleportUsesRadiusClearanceWhenLargerThanOneTile(t *testing.T) {
	state := newLilySkillState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}, Radius: 0.8},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}, Radius: 0.7},
	}, MapData{})

	snapshot := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)

	assertVectorNear(t, "radius clearance", playerByID(t, snapshot, "lily").Pos, Vector2{X: 1.500001}, 1e-9)
}

func TestLilyTeleportCancelsWhenEntireClearanceIntervalIsBlocked(t *testing.T) {
	for _, blocker := range []struct {
		name    string
		players []PlayerData
		gameMap MapData
	}{
		{
			name: "live player",
			players: []PlayerData{
				{ID: "blocker", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 1.5}},
			},
		},
		{name: "wall", gameMap: lineMapWithTile(4, TileWall)},
		{name: "water", gameMap: lineMapWithTile(4, TileWater)},
	} {
		t.Run(blocker.name, func(t *testing.T) {
			players := []PlayerData{
				{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
				{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
			}
			players = append(players, blocker.players...)
			state := newLilySkillState(players, blocker.gameMap)

			snapshot := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)

			assertVectorNear(t, "blocked teleport", playerByID(t, snapshot, "lily").Pos, Vector2{X: -2}, 1e-9)
			assertPlayerHP(t, snapshot, "target", DefaultPlayerHP-400, false)
		})
	}
}

func TestLilyTeleportIgnoresDeadPlayerBlocker(t *testing.T) {
	state := newLilySkillState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
		{ID: "dead-blocker", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 1.2}, HP: 1, IsDead: true},
	}, MapData{})

	snapshot := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)

	assertVectorNear(t, "dead blocker ignored", playerByID(t, snapshot, "lily").Pos, Vector2{X: TileSize}, 1e-9)
}

func TestLilySeedUsesModeEligibilityAndRangeBoundary(t *testing.T) {
	tests := []struct {
		name   string
		target PlayerData
		wantHP float64
	}{
		{
			name:   "ally is not eligible",
			target: PlayerData{ID: "target", Team: TeamRed, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
			wantHP: DefaultPlayerHP,
		},
		{
			name:   "range endpoint tangent hits",
			target: PlayerData{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 10.4*TileSize + DefaultPlayerRadius + DefaultProjectileRadius}},
			wantHP: DefaultPlayerHP - 400,
		},
		{
			name:   "just beyond range does not hit",
			target: PlayerData{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 10.4*TileSize + DefaultPlayerRadius + DefaultProjectileRadius + 1e-6}},
			wantHP: DefaultPlayerHP,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newLilySkillState([]PlayerData{
				{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily},
				tt.target,
			}, MapData{})

			snapshot := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)

			assertPlayerHP(t, snapshot, "target", tt.wantHP, false)
		})
	}
}

func TestLilySeedMapCollisionUsesProjectilePolicy(t *testing.T) {
	for _, test := range []struct {
		name   string
		tile   TileType
		wantHP float64
	}{
		{name: "wall blocks", tile: TileWall, wantHP: DefaultPlayerHP},
		{name: "water passes", tile: TileWater, wantHP: DefaultPlayerHP - 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newLilySkillState([]PlayerData{
				{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2.5}},
				{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 1.5}},
			}, lineMapWithTile(3, test.tile))

			snapshot := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)

			assertPlayerHP(t, snapshot, "target", test.wantHP, false)
		})
	}
}

func TestLilyTeleportBacksOffFromMapBoundary(t *testing.T) {
	gameMap := lineMapWithTile(0, TileGround)
	gameMap.TileSize = TileSize
	state := newLilySkillState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 2.6}},
	}, gameMap)

	snapshot := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)

	assertVectorNear(t, "boundary backoff", playerByID(t, snapshot, "lily").Pos, Vector2{X: 3.7}, 1e-9)
}

func TestLilyTeleportTileCornerUsesCircleVsTileGeometry(t *testing.T) {
	rows := make([][]TileType, 5)
	for y := range rows {
		rows[y] = make([]TileType, 5)
	}
	rows[1][3] = TileWall
	gameMap := MapData{Width: 5, Height: 5, MaxPlayers: 6, TileSize: 1, Map: rows}
	state := newLilySkillState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2, Y: -2}},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: -1, Y: -1}},
	}, gameMap)
	desired := Vector2{X: 0.1, Y: 1.9}
	direction := normalizeDirection(Vector2{X: desired.X + 1, Y: desired.Y + 1})
	desiredDistance := math.Hypot(desired.X+1, desired.Y+1)

	distance, ok := state.largestValidTeleportDistance(Vector2{X: -1, Y: -1}, direction, DefaultPlayerRadius, 0.1, desiredDistance, 0, 1)

	if !ok || math.Abs(distance-desiredDistance) > 1e-9 {
		t.Fatalf("corner-safe distance=%v ok=%t, want desired %v", distance, ok, desiredDistance)
	}
	if state.collidesWithMap(desired, DefaultPlayerRadius, tileBlocksPlayer) {
		t.Fatalf("fixture destination %+v unexpectedly collides with rounded tile corner", desired)
	}
}

func TestLilySeedChoosesLowestPlayerIDAtSimultaneousContact(t *testing.T) {
	players := []PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
		{ID: "z-target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
		{ID: "a-target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
	}
	state := newLilySkillState(players, MapData{})
	reversedState := newLilySkillState([]PlayerData{players[0], players[2], players[1]}, MapData{})

	forward := activateAndResolveLilySeed(t, state, "lily", Vector2{X: 1}, nil)
	reversed := activateAndResolveLilySeed(t, reversedState, "lily", Vector2{X: 1}, nil)

	assertPlayerHP(t, forward, "a-target", DefaultPlayerHP-400, false)
	assertPlayerHP(t, forward, "z-target", DefaultPlayerHP, false)
	if !reflect.DeepEqual(playersByID(forward.Players), playersByID(reversed.Players)) {
		t.Fatalf("result differs by player slice order:\nforward=%+v\nreversed=%+v", forward.Players, reversed.Players)
	}
}

func TestLilyTeleportSettlesBeforeSameTickMovement(t *testing.T) {
	state := newLilySkillState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{}},
	}, MapData{})
	state.Step([]InputCommand{{PlayerID: "lily", AttackDir: Vector2{X: 1}, PressedSkill: true}})
	state.Step(nil)
	state.Step(nil)

	snapshot := state.Step([]InputCommand{{PlayerID: "lily", ClientTick: 2, MoveDir: Vector2{X: 1}}})

	want := Vector2{X: TileSize + DefaultPlayerSpeed/TickRate}
	assertVectorNear(t, "post-teleport movement", playerByID(t, snapshot, "lily").Pos, want, 1e-9)
}

func newLilySkillState(players []PlayerData, gameMap MapData) *State {
	gameConfig := StaticGameConfig()
	gameConfig.Map = gameMap
	return NewStateWithConfig(players, Config{Game: gameConfig})
}

func activateAndResolveLilySeed(t *testing.T, state *State, ownerID PlayerID, direction Vector2, hitInputs []InputCommand) Snapshot {
	t.Helper()
	state.Step([]InputCommand{{PlayerID: ownerID, AttackDir: direction, PressedSkill: true}})
	return resolveLilySeed(t, state, hitInputs)
}

func resolveLilySeed(t *testing.T, state *State, hitInputs []InputCommand) Snapshot {
	t.Helper()
	for step := 0; step < 40; step++ {
		inputs := []InputCommand(nil)
		if hitInputs != nil {
			inputs = hitInputs
		}
		snapshot := state.Step(inputs)
		for _, projectile := range snapshot.Projectiles {
			if projectile.Type == "lily_seed" && projectile.IsDestroyed {
				return snapshot
			}
		}
	}
	t.Fatal("Lily seed did not resolve within 40 ticks")
	return Snapshot{}
}

func assertVectorNear(t *testing.T, label string, got, want Vector2, epsilon float64) {
	t.Helper()
	if math.Abs(got.X-want.X) > epsilon || math.Abs(got.Y-want.Y) > epsilon {
		t.Fatalf("%s = %+v, want %+v", label, got, want)
	}
}

func playersByID(players []PlayerData) map[PlayerID]PlayerData {
	result := make(map[PlayerID]PlayerData, len(players))
	for _, player := range players {
		result[player.ID] = player
	}
	return result
}
