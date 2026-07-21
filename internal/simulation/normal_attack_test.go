package simulation

import "testing"

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
			state := NewStateWithConfig([]PlayerData{{
				ID:            "player",
				CharacterType: tt.character,
			}}, Config{})

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
	return NewStateWithConfig(players, Config{Game: gameConfig})
}

func assertAttackCharges(t *testing.T, state *State, wants map[PlayerID]int) {
	t.Helper()
	for playerID, want := range wants {
		if got := state.attackStates[playerID].charges; got != want {
			t.Fatalf("%s charges = %d, want %d", playerID, got, want)
		}
	}
}
