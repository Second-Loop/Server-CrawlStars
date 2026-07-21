package simulation

import (
	"math"
	"reflect"
	"strconv"
	"testing"
)

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
	state := NewStateWithConfig(players, config)
	// Movement and collision tests isolate one centered pellet after config validation;
	// public/default behavior tests continue to exercise Shelly's configured spread.
	for index := range state.gameConfig.Player.Types {
		attack := &state.gameConfig.Player.Types[index].NormalAttack
		if state.gameConfig.Player.Types[index].CharacterType != CharacterTypeShelly || attack.Projectile == nil {
			continue
		}
		attack.Projectile.Count = 1
		attack.Projectile.DirectionOffsetsDegrees = []float64{0}
	}
	return state
}

func assertAttackCharges(t *testing.T, state *State, wants map[PlayerID]int) {
	t.Helper()
	for playerID, want := range wants {
		if got := state.attackStates[playerID].charges; got != want {
			t.Fatalf("%s charges = %d, want %d", playerID, got, want)
		}
	}
}
