package simulation

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"testing"
)

func TestSegmentCircleHit(t *testing.T) {
	tests := []struct {
		name   string
		end    Vector2
		center Vector2
		radius float64
		want   bool
	}{
		{"inside", Vector2{X: 2}, Vector2{X: 1}, 0.25, true},
		{"endpoint tangent", Vector2{X: 1}, Vector2{X: 1.5}, 0.5, true},
		{"lateral tangent", Vector2{X: 2}, Vector2{X: 1, Y: 0.5}, 0.5, true},
		{"epsilon outside", Vector2{X: 2}, Vector2{X: 1, Y: 0.500001}, 0.5, false},
		{"tiny segment clear miss", Vector2{X: 1e-8}, Vector2{Y: 1}, 0.5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := segmentCircleHit(Vector2{}, tt.end, tt.center, tt.radius)
			if got != tt.want {
				t.Fatalf("hit = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestSegmentAABBHit(t *testing.T) {
	tests := []struct {
		name  string
		start Vector2
		end   Vector2
		min   Vector2
		max   Vector2
		wantT float64
		want  bool
	}{
		{"hit", Vector2{}, Vector2{X: 2}, Vector2{X: 1, Y: -0.5}, Vector2{X: 1.5, Y: 0.5}, 0.5, true},
		{"miss", Vector2{}, Vector2{X: 2}, Vector2{X: 1, Y: 0.5}, Vector2{X: 1.5, Y: 1}, 0, false},
		{"start inside", Vector2{X: 1.25}, Vector2{X: 2}, Vector2{X: 1, Y: -0.5}, Vector2{X: 1.5, Y: 0.5}, 0, true},
		{"corner tangent", Vector2{}, Vector2{X: 2, Y: 2}, Vector2{X: 1, Y: 1}, Vector2{X: 1.5, Y: 1.5}, 0.5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotT, got := segmentAABBHit(tt.start, tt.end, tt.min, tt.max)
			if got != tt.want {
				t.Fatalf("hit = %t, want %t", got, tt.want)
			}
			if got && math.Abs(gotT-tt.wantT) > 1e-12 {
				t.Fatalf("t = %v, want %v", gotT, tt.wantT)
			}
		})
	}
}

func TestLilyCenterlineRangeAndTangency(t *testing.T) {
	const rangeDistance = 2.2 * TileSize
	tests := []struct {
		name       string
		position   Vector2
		wantDamage bool
	}{
		{"inside 2.2 tiles", Vector2{X: rangeDistance - 0.01}, true},
		{"just outside", Vector2{X: rangeDistance + DefaultPlayerRadius + 0.000001}, false},
		{"endpoint tangent", Vector2{X: rangeDistance + DefaultPlayerRadius}, true},
		{"lateral tangent", Vector2{X: 1, Y: DefaultPlayerRadius}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newLilyTestState([]PlayerData{
				{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily},
				{ID: "target", Team: TeamBlue, Pos: tt.position},
			}, MapData{})

			snapshot := state.Step([]InputCommand{lilyAttackInput("lily", Vector2{X: 1})})

			wantHP := DefaultPlayerHP
			if tt.wantDamage {
				wantHP -= 1100
			}
			assertPlayerHP(t, snapshot, "target", wantHP, false)
		})
	}
}

func TestLilyWallContactPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		wallIndex  int
		targetPos  Vector2
		wantDamage bool
	}{
		{"wall before target blocks", 3, Vector2{X: 1}, false},
		{"wall behind target does not block", 4, Vector2{}, true},
		{"equal contact lets wall win", 3, Vector2{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newLilyTestState([]PlayerData{
				{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -1.5}},
				{ID: "target", Team: TeamBlue, Pos: tt.targetPos},
			}, lineMapWithTile(tt.wallIndex, TileWall))

			snapshot := state.Step([]InputCommand{lilyAttackInput("lily", Vector2{X: 1})})

			wantHP := DefaultPlayerHP
			if tt.wantDamage {
				wantHP -= 1100
			}
			assertPlayerHP(t, snapshot, "target", wantHP, false)
		})
	}
}

func TestLilyBushAndWaterDoNotBlock(t *testing.T) {
	for _, tile := range []TileType{TileBush, TileWater} {
		t.Run(strconv.Itoa(int(tile)), func(t *testing.T) {
			state := newLilyTestState([]PlayerData{
				{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -1.5}},
				{ID: "target", Team: TeamBlue, Pos: Vector2{X: 1}},
			}, lineMapWithTile(3, tile))

			snapshot := state.Step([]InputCommand{lilyAttackInput("lily", Vector2{X: 1})})

			assertPlayerHP(t, snapshot, "target", DefaultPlayerHP-1100, false)
		})
	}
}

func TestLilyBoundaryTruncatesCenterline(t *testing.T) {
	gameMap := MapData{
		Width: 4, Height: 4, MaxPlayers: 6, TileSize: 1,
		Map: [][]TileType{
			{TileGround, TileGround, TileGround, TileGround},
			{TileGround, TileGround, TileGround, TileGround},
			{TileGround, TileGround, TileGround, TileGround},
			{TileGround, TileGround, TileGround, TileGround},
		},
	}
	state := newLilyTestState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: 1.5}},
		{ID: "target", Team: TeamBlue, Pos: Vector2{X: 2.2}, Radius: 0.1},
	}, gameMap)

	snapshot := state.Step([]InputCommand{lilyAttackInput("lily", Vector2{X: 1})})

	assertPlayerHP(t, snapshot, "target", DefaultPlayerHP, false)
}

func TestLilyDiagonalWallAndBoundaryEqualityPreferBlockingContact(t *testing.T) {
	diagonal := math.Sqrt(0.5)
	ground := func(size int) [][]TileType {
		rows := make([][]TileType, size)
		for y := range rows {
			rows[y] = make([]TileType, size)
		}
		return rows
	}
	tests := []struct {
		name      string
		origin    Vector2
		targetPos Vector2
		gameMap   MapData
	}{
		{
			name:      "wall corner equality",
			origin:    Vector2{X: -2, Y: -1.5},
			targetPos: Vector2{X: -0.5 + diagonal*DefaultPlayerRadius, Y: diagonal * DefaultPlayerRadius},
			gameMap: func() MapData {
				rows := ground(5)
				rows[2][2] = TileWall
				return MapData{Width: 5, Height: 5, MaxPlayers: 6, TileSize: 1, Map: rows}
			}(),
		},
		{
			name:      "boundary tangent equality",
			origin:    Vector2{X: 2.5 - 0.895*2.2*diagonal, Y: -0.895 * 2.2 * diagonal},
			targetPos: Vector2{X: 2.5 + diagonal*DefaultPlayerRadius, Y: -diagonal * DefaultPlayerRadius},
			gameMap:   MapData{Width: 5, Height: 5, MaxPlayers: 6, TileSize: 1, Map: ground(5)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newLilyTestState([]PlayerData{
				{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: tt.origin},
				{ID: "target", Team: TeamBlue, Pos: tt.targetPos},
			}, tt.gameMap)
			direction := normalizeDirection(Vector2{X: 1, Y: 1})
			attack, ok := state.normalAttackConfig("lily")
			if !ok {
				t.Fatal("Lily normal attack config is missing")
			}
			end := Vector2{
				X: tt.origin.X + direction.X*attack.RangeTiles*state.resolvedTileSize(),
				Y: tt.origin.Y + direction.Y*attack.RangeTiles*state.resolvedTileSize(),
			}
			targetT, hit := segmentCircleHit(tt.origin, end, tt.targetPos, state.players[1].Radius)
			blockingT := state.firstBlockingSegmentT(tt.origin, end)
			if !hit || math.Abs(targetT-blockingT) > meleeContactEpsilon {
				t.Fatalf("fixture contact targetT=%v blockingT=%v, want equality within %v", targetT, blockingT, meleeContactEpsilon)
			}
			snapshot := state.Step([]InputCommand{lilyAttackInput("lily", Vector2{X: 1, Y: 1})})

			assertPlayerHP(t, snapshot, "target", DefaultPlayerHP, false)
		})
	}
}

func TestLilyHitEligibilityMatchesModeRules(t *testing.T) {
	tests := []struct {
		name       string
		rules      GameModeRulesConfig
		target     PlayerData
		wantDamage bool
	}{
		{
			name:   "owner excluded",
			rules:  GameModeRulesConfig{TeamBehavior: TeamBehaviorFreeForAll},
			target: PlayerData{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily},
		},
		{
			name:   "dead enemy excluded",
			rules:  GameModeRulesConfig{TeamBehavior: TeamBehaviorTwoTeams},
			target: PlayerData{ID: "target", Team: TeamBlue, Pos: Vector2{X: 1}, HP: 100, IsDead: true},
		},
		{
			name:   "ally excluded when friendly fire is off",
			rules:  GameModeRulesConfig{TeamBehavior: TeamBehaviorTwoTeams},
			target: PlayerData{ID: "target", Team: TeamRed, Pos: Vector2{X: 1}},
		},
		{
			name:       "ally included when friendly fire is on",
			rules:      GameModeRulesConfig{TeamBehavior: TeamBehaviorTwoTeams, FriendlyFire: true},
			target:     PlayerData{ID: "target", Team: TeamRed, Pos: Vector2{X: 1}},
			wantDamage: true,
		},
		{
			name:       "free for all ignores matching team label",
			rules:      GameModeRulesConfig{TeamBehavior: TeamBehaviorFreeForAll},
			target:     PlayerData{ID: "target", Team: TeamRed, Pos: Vector2{X: 1}},
			wantDamage: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			players := []PlayerData{{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily}}
			if tt.target.ID != "lily" {
				players = append(players, tt.target)
			}
			state := newLilyTestState(players, MapData{})
			state.gameConfig.SelectedMode.Rules = tt.rules

			snapshot := state.Step([]InputCommand{lilyAttackInput("lily", Vector2{X: 1})})

			if tt.target.ID == "lily" {
				assertPlayerHP(t, snapshot, "lily", 4100, false)
				return
			}
			wantHP := tt.target.HP
			if wantHP <= 0 {
				wantHP = DefaultPlayerHP
			}
			if tt.wantDamage {
				wantHP -= 1100
			}
			assertPlayerHP(t, snapshot, "target", wantHP, tt.target.IsDead)
		})
	}
}

func TestLilyFirstCanonicalTargetWinsBeforeNearerTarget(t *testing.T) {
	state := newLilyTestState([]PlayerData{
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily},
		{ID: "farther-first", Team: TeamBlue, Pos: Vector2{X: 2}},
		{ID: "nearer-later", Team: TeamBlue, Pos: Vector2{X: 1}},
	}, MapData{})

	snapshot := state.Step([]InputCommand{lilyAttackInput("lily", Vector2{X: 1})})

	assertPlayerHP(t, snapshot, "farther-first", DefaultPlayerHP-1100, false)
	assertPlayerHP(t, snapshot, "nearer-later", DefaultPlayerHP, false)
}

func TestLilyMissAndWallBlockConsumeCharge(t *testing.T) {
	tests := []struct {
		name    string
		players []PlayerData
		gameMap MapData
	}{
		{
			name:    "miss",
			players: []PlayerData{{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily}},
		},
		{
			name: "wall block",
			players: []PlayerData{
				{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -1.5}},
				{ID: "target", Team: TeamBlue, Pos: Vector2{X: 1}},
			},
			gameMap: lineMapWithTile(3, TileWall),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newLilyTestState(tt.players, tt.gameMap)
			before := state.attackStates["lily"].charges

			snapshot := state.Step([]InputCommand{lilyAttackInput("lily", Vector2{X: 1})})

			if !snapshot.Players[0].PressedAttack {
				t.Fatal("accepted Lily attack must set PressedAttack")
			}
			if got := state.attackStates["lily"].charges; got != before-1 {
				t.Fatalf("charges = %d, want %d", got, before-1)
			}
		})
	}
}

func TestLilyTargetSelectionWaitsForAllMovement(t *testing.T) {
	state := newLilyTestState([]PlayerData{
		{ID: "a-lily", Team: TeamRed, CharacterType: CharacterTypeLily},
		{ID: "z-target", Team: TeamBlue, Pos: Vector2{X: 2.2*TileSize + DefaultPlayerRadius}},
	}, MapData{})

	snapshot := state.Step([]InputCommand{
		lilyAttackInput("a-lily", Vector2{X: 1}),
		{PlayerID: "z-target", MoveDir: Vector2{X: 1}},
	})

	assertPlayerHP(t, snapshot, "z-target", DefaultPlayerHP, false)
}

func TestLilyMutualKillIsDeterministicAcrossInputOrder(t *testing.T) {
	players := []PlayerData{
		{ID: "lily-a", Team: TeamRed, CharacterType: CharacterTypeLily, HP: 1100},
		{ID: "lily-b", Team: TeamBlue, CharacterType: CharacterTypeLily, Pos: Vector2{X: 2}, HP: 1100},
	}
	inputs := []InputCommand{
		lilyAttackInput("lily-a", Vector2{X: 1}),
		lilyAttackInput("lily-b", Vector2{X: -1}),
	}
	state := newLilyTestState(players, MapData{})
	reversedState := newLilyTestState(players, MapData{})

	snapshot := state.Step(inputs)
	reversedSnapshot := reversedState.Step([]InputCommand{inputs[1], inputs[0]})

	if !reflect.DeepEqual(snapshot, reversedSnapshot) {
		t.Fatalf("snapshot differs by input order:\nfirst: %+v\nreversed: %+v", snapshot, reversedSnapshot)
	}
	assertPlayerHP(t, snapshot, "lily-a", 0, true)
	assertPlayerHP(t, snapshot, "lily-b", 0, true)
}

func newLilyTestState(players []PlayerData, gameMap MapData) *State {
	gameConfig := StaticGameConfig()
	gameConfig.Map = gameMap
	return NewStateWithConfig(players, Config{Game: gameConfig})
}

func lilyAttackInput(playerID PlayerID, direction Vector2) InputCommand {
	return InputCommand{PlayerID: playerID, AttackDir: direction, PressedAttack: true}
}

func lineMapWithTile(index int, tile TileType) MapData {
	rows := [][]TileType{
		{TileGround, TileGround, TileGround, TileGround, TileGround, TileGround, TileGround},
		{TileGround, TileGround, TileGround, TileGround, TileGround, TileGround, TileGround},
		{TileGround, TileGround, TileGround, TileGround, TileGround, TileGround, TileGround},
		{TileGround, TileGround, TileGround, TileGround, TileGround, TileGround, TileGround},
		{TileGround, TileGround, TileGround, TileGround, TileGround, TileGround, TileGround},
	}
	rows[2][index] = tile
	return MapData{Width: len(rows[0]), Height: len(rows), MaxPlayers: 6, TileSize: 1, Map: rows}
}

func TestShellyAttackEmitsConfiguredSpreadFromPostMovementPosition(t *testing.T) {
	state := NewState([]PlayerData{{
		ID:            "shelly",
		Team:          TeamRed,
		CharacterType: CharacterTypeShelly,
	}})

	snapshot := state.Step([]InputCommand{{
		PlayerID:      "shelly",
		MoveDir:       Vector2{Y: 1},
		AttackDir:     Vector2{X: 1},
		PressedAttack: true,
	}})

	if got := len(snapshot.Projectiles); got != 5 {
		t.Fatalf("Shelly projectiles = %d, want 5", got)
	}
	wantPosition := snapshot.Players[0].Pos
	wantOffsets := []float64{-12, -6, 0, 6, 12}
	for index, offset := range wantOffsets {
		projectile := snapshot.Projectiles[index]
		wantID := ProjectileID("projectile-1-shelly-" + strconv.Itoa(index+1))
		if projectile.ID != wantID {
			t.Errorf("projectile %d ID = %q, want %q", index, projectile.ID, wantID)
		}
		assertVector(t, "Shelly projectile position", projectile.Pos, wantPosition)
		radians := offset * math.Pi / 180
		if math.Abs(projectile.Dir.X-math.Cos(radians)) > 1e-12 || math.Abs(projectile.Dir.Y-math.Sin(radians)) > 1e-12 {
			t.Errorf("projectile %d direction = %+v, want angle %v degrees", index, projectile.Dir, offset)
		}
		if projectile.Damage != 280 {
			t.Errorf("projectile %d damage = %v, want 280", index, projectile.Damage)
		}
		runtime := state.projectileRuntime[projectile.ID]
		if math.Abs(runtime.maxDistance-8.64) > 1e-12 {
			t.Errorf("projectile %d max distance = %v, want 8.64", index, runtime.maxDistance)
		}
	}
}

func TestProjectileAttacksAcceptAlternateConfiguredCountsAndEmitThem(t *testing.T) {
	tests := []struct {
		name          string
		character     CharacterType
		configure     func(*ProjectileAttackConfig)
		steps         int
		wantEmissions int
	}{
		{
			name:      "three pellet spread",
			character: CharacterTypeShelly,
			configure: func(projectile *ProjectileAttackConfig) {
				projectile.Count = 3
				projectile.DirectionOffsetsDegrees = []float64{-10, 0, 10}
			},
			steps:         1,
			wantEmissions: 3,
		},
		{
			name:      "four shot burst",
			character: CharacterTypeColt,
			configure: func(projectile *ProjectileAttackConfig) {
				projectile.Count = 4
				projectile.DirectionOffsetsDegrees = []float64{0}
				projectile.IntervalTicks = 2
			},
			steps:         7,
			wantEmissions: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gameConfig := StaticGameConfig()
			gameConfig.Map = lineMapWithTile(0, TileGround)
			for index := range gameConfig.Player.Types {
				if gameConfig.Player.Types[index].CharacterType == tt.character {
					tt.configure(gameConfig.Player.Types[index].NormalAttack.Projectile)
				}
			}
			resolved, err := ResolveGameConfig(gameConfig)
			if err != nil {
				t.Fatalf("ResolveGameConfig() error = %v, want alternate count accepted", err)
			}
			state := NewStateWithConfig([]PlayerData{{
				ID:            "owner",
				Team:          TeamRed,
				CharacterType: tt.character,
				Pos:           Vector2{X: -1.5},
			}}, Config{Game: resolved})

			var snapshot Snapshot
			for step := 1; step <= tt.steps; step++ {
				var inputs []InputCommand
				if step == 1 {
					inputs = []InputCommand{{PlayerID: "owner", AttackDir: Vector2{X: 1}, PressedAttack: true}}
				}
				snapshot = state.Step(inputs)
			}
			if got := len(snapshot.Projectiles); got != tt.wantEmissions {
				t.Fatalf("projectile emissions = %d, want %d", got, tt.wantEmissions)
			}
		})
	}
}

func TestSpreadProjectileHugeFiniteOffsetEmitsFiniteMarshalableSnapshot(t *testing.T) {
	gameConfig := StaticGameConfig()
	gameConfig.Map = lineMapWithTile(0, TileGround)
	for index := range gameConfig.Player.Types {
		if gameConfig.Player.Types[index].CharacterType != CharacterTypeShelly {
			continue
		}
		gameConfig.Player.Types[index].NormalAttack.Projectile.Count = 1
		gameConfig.Player.Types[index].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{math.MaxFloat64}
	}
	resolved, err := ResolveGameConfig(gameConfig)
	if err != nil {
		t.Fatalf("ResolveGameConfig() error = %v, want finite offset accepted", err)
	}
	state := NewStateWithConfig([]PlayerData{{
		ID:            "shelly",
		Team:          TeamRed,
		CharacterType: CharacterTypeShelly,
		Pos:           Vector2{X: -1.5},
	}}, Config{Game: resolved})

	snapshot := state.Step([]InputCommand{{
		PlayerID:      "shelly",
		AttackDir:     Vector2{X: 1},
		PressedAttack: true,
	}})

	if got := len(snapshot.Projectiles); got != 1 {
		t.Fatalf("projectiles = %d, want 1", got)
	}
	direction := snapshot.Projectiles[0].Dir
	if math.IsNaN(direction.X) || math.IsInf(direction.X, 0) || math.IsNaN(direction.Y) || math.IsInf(direction.Y, 0) {
		t.Errorf("projectile direction = %+v, want finite X/Y", direction)
	}
	if _, err := json.Marshal(snapshot); err != nil {
		t.Errorf("json.Marshal(snapshot) error = %v", err)
	}
}

func TestColtBurstEmitsOnConfiguredTicksFromCurrentPositionWithFixedDirection(t *testing.T) {
	state := NewState([]PlayerData{{
		ID:            "colt",
		Team:          TeamRed,
		CharacterType: CharacterTypeColt,
	}})

	dueTicks := map[Tick]bool{1: true, 7: true, 13: true, 19: true, 25: true, 31: true}
	previousCount := 0
	for inputTick := Tick(1); inputTick <= 31; inputTick++ {
		attackDirection := Vector2{Y: 1}
		if inputTick == 1 {
			attackDirection = Vector2{X: 1}
		}
		snapshot := state.Step([]InputCommand{{
			PlayerID:      "colt",
			ClientTick:    int64(inputTick),
			MoveDir:       Vector2{Y: 1},
			AttackDir:     attackDirection,
			PressedAttack: inputTick == 1,
		}})

		if got := snapshot.Players[0].LastProcessedClientTick; got != int64(inputTick) {
			t.Fatalf("snapshot %d ACK = %d, want %d", inputTick, got, inputTick)
		}
		wantCount := previousCount
		if dueTicks[inputTick] {
			wantCount++
		}
		if got := len(snapshot.Projectiles); got != wantCount {
			t.Fatalf("snapshot %d projectiles = %d, want %d", inputTick, got, wantCount)
		}
		if dueTicks[inputTick] {
			projectile := snapshot.Projectiles[len(snapshot.Projectiles)-1]
			assertVector(t, "Colt projectile position", projectile.Pos, snapshot.Players[0].Pos)
			assertVector(t, "Colt fixed burst direction", projectile.Dir, Vector2{X: 1})
		}
		previousCount = wantCount
	}
}

func TestColtBurstAttackApprovalTable(t *testing.T) {
	gameConfig := StaticGameConfig()
	gameConfig.Map = MapData{}
	for index := range gameConfig.Player.Types {
		if gameConfig.Player.Types[index].CharacterType == CharacterTypeColt {
			gameConfig.Player.Types[index].NormalAttack.RechargeTicks = 1000
		}
	}
	state := NewStateWithConfig([]PlayerData{{
		ID:            "colt",
		Team:          TeamRed,
		CharacterType: CharacterTypeColt,
	}}, Config{Game: gameConfig})

	tests := []struct {
		name             string
		inputTick        Tick
		wantPressed      bool
		wantChargeChange bool
	}{
		{"activation", 1, true, true},
		{"mid burst", 7, false, false},
		{"last emission", 31, false, false},
		{"next tick", 32, true, true},
	}

	currentTick := Tick(0)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for currentTick+1 < tt.inputTick {
				state.Step(nil)
				currentTick++
			}
			beforeCharges := state.attackStates["colt"].charges
			snapshot := state.Step([]InputCommand{{
				PlayerID:      "colt",
				ClientTick:    int64(tt.inputTick),
				AttackDir:     Vector2{X: 1},
				PressedAttack: true,
			}})
			currentTick++

			if got := snapshot.Players[0].PressedAttack; got != tt.wantPressed {
				t.Errorf("PressedAttack = %t, want %t", got, tt.wantPressed)
			}
			chargeChanged := state.attackStates["colt"].charges != beforeCharges
			if chargeChanged != tt.wantChargeChange {
				t.Errorf("charge changed = %t, want %t (before=%d after=%d)", chargeChanged, tt.wantChargeChange, beforeCharges, state.attackStates["colt"].charges)
			}
			if got := snapshot.Players[0].LastProcessedClientTick; got != int64(tt.inputTick) {
				t.Errorf("ACK = %d, want %d", got, tt.inputTick)
			}
		})
	}
}

func TestColtBurstCancelsDueEmissionWhenOwnerDiesInPrePhase(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt, HP: 100},
		{ID: "enemy", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 100}},
	})
	activated := state.Step([]InputCommand{{
		PlayerID:      "colt",
		AttackDir:     Vector2{X: 1},
		PressedAttack: true,
	}})
	firstBulletID := activated.Projectiles[0].ID
	for snapshotTick := Tick(2); snapshotTick < 7; snapshotTick++ {
		state.Step(nil)
	}
	state.projectiles = append(state.projectiles, ProjectileData{
		ID:      "killer",
		OwnerID: "enemy",
		Pos:     Vector2{},
		Damage:  100,
		Radius:  0.3,
	})

	snapshot := state.Step(nil)
	if !snapshot.Players[0].IsDead {
		t.Fatal("Colt must die during the pre-phase projectile movement")
	}
	if got := len(snapshot.Projectiles); got != 2 {
		t.Fatalf("projectiles after death = %d, want first bullet and killer only", got)
	}
	if snapshot.Projectiles[0].ID != firstBulletID {
		t.Fatalf("already-fired bullet %q was not retained: %+v", firstBulletID, snapshot.Projectiles)
	}
	if _, active := state.burstStates["colt"]; active {
		t.Fatal("dead Colt burst must be deleted")
	}
}

func TestColtDueEmissionSurvivesSamePhaseLilyKillThenFutureBurstCancels(t *testing.T) {
	state := NewState([]PlayerData{
		{ID: "colt", Team: TeamBlue, CharacterType: CharacterTypeColt, HP: 1100},
		{ID: "lily", Team: TeamRed, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
	})
	state.Step([]InputCommand{{
		PlayerID:      "colt",
		AttackDir:     Vector2{Y: 1},
		PressedAttack: true,
	}})
	for snapshotTick := Tick(2); snapshotTick < 7; snapshotTick++ {
		state.Step(nil)
	}

	killSnapshot := state.Step([]InputCommand{lilyAttackInput("lily", Vector2{X: 1})})

	assertPlayerHP(t, killSnapshot, "colt", 0, true)
	if got := len(killSnapshot.Projectiles); got != 2 {
		t.Fatalf("projectiles on due-and-kill tick = %d, want activation and already-due emissions", got)
	}
	if killSnapshot.Projectiles[1].OwnerID != "colt" {
		t.Fatalf("due projectile owner = %q, want colt", killSnapshot.Projectiles[1].OwnerID)
	}
	for snapshotTick := Tick(8); snapshotTick <= 13; snapshotTick++ {
		state.Step(nil)
	}
	if got := len(state.projectiles); got != 2 {
		t.Fatalf("projectiles after future due tick = %d, want no future Colt emission", got)
	}
	if _, active := state.burstStates["colt"]; active {
		t.Fatal("dead Colt future burst must be canceled")
	}
}

func TestNormalAttackInputOrderKeepsColtBurstSnapshotsDeterministic(t *testing.T) {
	players := []PlayerData{
		{ID: "colt-b", Team: TeamRed, CharacterType: CharacterTypeColt, Pos: Vector2{X: 10}},
		{ID: "colt-a", Team: TeamRed, CharacterType: CharacterTypeColt, Pos: Vector2{X: -10}},
	}
	state := NewState(players)
	reversedState := NewState(players)

	for inputTick := Tick(1); inputTick <= 31; inputTick++ {
		inputs := []InputCommand{
			{PlayerID: "colt-b", ClientTick: int64(inputTick), MoveDir: Vector2{Y: -1}, AttackDir: Vector2{X: -1}, PressedAttack: inputTick == 1},
			{PlayerID: "colt-a", ClientTick: int64(inputTick), MoveDir: Vector2{Y: 1}, AttackDir: Vector2{X: 1}, PressedAttack: inputTick == 1},
		}
		reversedInputs := []InputCommand{inputs[1], inputs[0]}
		snapshot := state.Step(inputs)
		reversedSnapshot := reversedState.Step(reversedInputs)
		if !reflect.DeepEqual(snapshot, reversedSnapshot) {
			t.Fatalf("snapshot %d differs by input order:\nfirst: %+v\nreversed: %+v", inputTick, snapshot, reversedSnapshot)
		}
	}
}

func TestAttackStateUsesCharacterChargeCapacity(t *testing.T) {
	state := NewStateWithConfig([]PlayerData{
		{ID: "s", CharacterType: CharacterTypeShelly},
		{ID: "c", CharacterType: CharacterTypeColt},
		{ID: "l", CharacterType: CharacterTypeLily},
	}, Config{})
	wants := map[PlayerID]int{"s": 3, "c": 3, "l": 2}
	for id, want := range wants {
		if got := state.attackStates[id].charges; got != want {
			t.Fatalf("%s charges = %d, want %d", id, got, want)
		}
	}
}

func TestStepAcceptsConfiguredCharacterChargeCounts(t *testing.T) {
	for _, tt := range []struct {
		name      string
		character CharacterType
		want      int
	}{
		{name: "Shelly", character: CharacterTypeShelly, want: 3},
		{name: "Colt", character: CharacterTypeColt, want: 3},
		{name: "Lily", character: CharacterTypeLily, want: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gameConfig := StaticGameConfig()
			gameConfig.Map = MapData{}
			for index := range gameConfig.Player.Types {
				if gameConfig.Player.Types[index].CharacterType == tt.character {
					gameConfig.Player.Types[index].NormalAttack.RechargeTicks = 1000
				}
			}
			state := NewStateWithConfig([]PlayerData{{
				ID:            "player",
				CharacterType: tt.character,
			}}, Config{Game: gameConfig})

			accepted := 0
			for range tt.want + 1 {
				snapshot := state.Step([]InputCommand{{
					PlayerID:      "player",
					AttackDir:     Vector2{X: 1},
					PressedAttack: true,
				}})
				if snapshot.Players[0].PressedAttack {
					accepted++
				}
				if tt.character == CharacterTypeColt && snapshot.Players[0].PressedAttack {
					for range 30 {
						state.Step(nil)
					}
				}
			}
			if accepted != tt.want {
				t.Fatalf("accepted attacks = %d, want %d", accepted, tt.want)
			}
		})
	}
}

func TestStepRechargesEachCharacterUsingOwnConfig(t *testing.T) {
	gameConfig := StaticGameConfig()
	gameConfig.Map = MapData{}
	for index := range gameConfig.Player.Types {
		gameConfig.Player.Types[index].NormalAttack.MaxCharges = 1
		switch gameConfig.Player.Types[index].CharacterType {
		case CharacterTypeShelly:
			gameConfig.Player.Types[index].NormalAttack.RechargeTicks = 1
		case CharacterTypeColt:
			gameConfig.Player.Types[index].NormalAttack.RechargeTicks = 2
		case CharacterTypeLily:
			gameConfig.Player.Types[index].NormalAttack.RechargeTicks = 3
		}
	}
	state := NewStateWithConfig([]PlayerData{
		{ID: "s", CharacterType: CharacterTypeShelly},
		{ID: "c", CharacterType: CharacterTypeColt},
		{ID: "l", CharacterType: CharacterTypeLily},
	}, Config{Game: gameConfig})

	exhausted := state.Step([]InputCommand{
		{PlayerID: "s", AttackDir: Vector2{X: 1}, PressedAttack: true},
		{PlayerID: "c", AttackDir: Vector2{X: 1}, PressedAttack: true},
		{PlayerID: "l", AttackDir: Vector2{X: 1}, PressedAttack: true},
	})
	for _, player := range exhausted.Players {
		if !player.PressedAttack {
			t.Fatalf("expected %s exhaustion attack to be accepted", player.ID)
		}
	}

	state.Step(nil)
	assertAttackCharges(t, state, map[PlayerID]int{"s": 1, "c": 0, "l": 0})
	state.Step(nil)
	assertAttackCharges(t, state, map[PlayerID]int{"s": 1, "c": 1, "l": 0})
	state.Step(nil)
	assertAttackCharges(t, state, map[PlayerID]int{"s": 1, "c": 1, "l": 1})
}

func TestResolvedTileSizeFallsBackForMaplessState(t *testing.T) {
	state := NewStateWithConfig([]PlayerData{{ID: "s", CharacterType: CharacterTypeShelly}}, Config{})
	if got := state.resolvedTileSize(); got != TileSize {
		t.Fatalf("tile size = %v, want %v", got, TileSize)
	}
}

func TestProjectileRangeClampsAndExpiresAtConfiguredDistance(t *testing.T) {
	state := newProjectileRangeState(1, nil)
	spawned := state.Step([]InputCommand{{
		PlayerID:      "owner",
		AttackDir:     Vector2{X: 1},
		PressedAttack: true,
	}})
	if len(spawned.Projectiles) != 1 {
		t.Fatalf("spawned projectiles = %d, want 1", len(spawned.Projectiles))
	}
	spawnedProjectile := spawned.Projectiles[0]
	assertVector(t, "spawn position", spawnedProjectile.Pos, Vector2{})
	if spawnedProjectile.Type != "range-test" || spawnedProjectile.Radius != 0.1 || spawnedProjectile.Speed != 1 || spawnedProjectile.Damage != 10 {
		t.Fatalf("config-owned projectile = %+v, want range-test type/radius/speed/damage", spawnedProjectile)
	}

	moved := state.Step(nil)
	assertVector(t, "first movement", moved.Projectiles[0].Pos, Vector2{X: TickDuration})
	if moved.Projectiles[0].IsDestroyed {
		t.Fatal("projectile destroyed before reaching configured range")
	}

	var final Snapshot
	for range TickRate - 1 {
		final = state.Step(nil)
	}
	projectile := final.Projectiles[0]
	assertVector(t, "range-clamped position", projectile.Pos, Vector2{X: 1})
	if !projectile.IsDestroyed {
		t.Fatal("missed projectile must be destroyed at configured range")
	}
	if _, ok := state.projectileRuntime[projectile.ID]; ok {
		t.Fatal("destroyed projectile runtime must be removed while tombstone remains public")
	}
}

func TestProjectileRangeHitsTangentTargetAtEndpoint(t *testing.T) {
	target := PlayerData{ID: "target", Team: TeamBlue, Pos: Vector2{X: 1.6}}
	state := newProjectileRangeState(1, &target)
	state.Step([]InputCommand{{
		PlayerID:      "owner",
		AttackDir:     Vector2{X: 1},
		PressedAttack: true,
	}})

	var final Snapshot
	for range TickRate {
		final = state.Step(nil)
	}
	projectile := final.Projectiles[0]
	assertVector(t, "endpoint hit position", projectile.Pos, Vector2{X: 1})
	if !projectile.IsDestroyed {
		t.Fatal("endpoint hit must destroy projectile")
	}
	assertPlayerHP(t, final, "target", DefaultPlayerHP-10, false)
}

func TestProjectileRangeUsesFallbackTileSizeForMaplessConfig(t *testing.T) {
	state := newProjectileRangeState(TileSize, nil)
	snapshot := state.Step([]InputCommand{{
		PlayerID:      "owner",
		AttackDir:     Vector2{X: 1},
		PressedAttack: true,
	}})
	projectile := snapshot.Projectiles[0]
	runtime, ok := state.projectileRuntime[projectile.ID]
	if !ok {
		t.Fatal("projectile range runtime is missing")
	}
	if runtime.maxDistance != TileSize {
		t.Fatalf("mapless max distance = %v, want %v", runtime.maxDistance, TileSize)
	}
}

func newProjectileRangeState(tileSize float64, target *PlayerData) *State {
	gameConfig := StaticGameConfig()
	gameConfig.Tile.Size = tileSize
	gameConfig.Map = MapData{}
	for index := range gameConfig.Player.Types {
		if gameConfig.Player.Types[index].CharacterType != CharacterTypeShelly {
			continue
		}
		gameConfig.Player.Types[index].NormalAttack = NormalAttackConfig{
			Kind:          NormalAttackSpreadProjectile,
			DamagePerHit:  10,
			RangeTiles:    1,
			MaxCharges:    3,
			RechargeTicks: 30,
			Projectile: &ProjectileAttackConfig{
				Type:                    "range-test",
				Count:                   5,
				DirectionOffsetsDegrees: []float64{-12, -6, 0, 6, 12},
			},
		}
	}
	gameConfig.Projectile.Types = []ProjectileTypeConfig{{ID: "range-test", Radius: 0.1, Speed: 1}}

	players := []PlayerData{{ID: "owner", Team: TeamRed, CharacterType: CharacterTypeShelly}}
	if target != nil {
		players = append(players, *target)
	}
	return newSingleProjectileTestState(players, Config{Game: gameConfig})
}

func newSingleProjectileTestState(players []PlayerData, config Config) *State {
	// Movement and collision tests resolve a valid one-pellet alternate config;
	// public/default behavior tests continue to exercise Shelly's configured spread.
	if config.Game.Version != ServerGameConfigVersion {
		config.Game = StaticGameConfig()
		if config.Map.Width == 0 && config.Map.Height == 0 && len(config.Map.Map) == 0 {
			config.Game.Map = MapData{}
		}
	}
	for index := range config.Game.Player.Types {
		attack := &config.Game.Player.Types[index].NormalAttack
		if config.Game.Player.Types[index].CharacterType != CharacterTypeShelly || attack.Projectile == nil {
			continue
		}
		attack.Projectile.Count = 1
		attack.Projectile.DirectionOffsetsDegrees = []float64{0}
	}
	return NewStateWithConfig(players, config)
}

func assertAttackCharges(t *testing.T, state *State, wants map[PlayerID]int) {
	t.Helper()
	for playerID, want := range wants {
		if got := state.attackStates[playerID].charges; got != want {
			t.Fatalf("%s charges = %d, want %d", playerID, got, want)
		}
	}
}
