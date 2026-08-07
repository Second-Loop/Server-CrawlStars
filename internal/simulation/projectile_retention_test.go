package simulation

import "testing"

func TestDestroyedProjectileTombstoneExpiresAfterExactly30GameplaySnapshots(t *testing.T) {
	state := newProjectileRangeState(1, nil)
	state.gameConfig.Projectile.Types[0].Speed = 100

	created := state.Step([]InputCommand{{
		PlayerID:      "owner",
		AttackDir:     Vector2{X: 1},
		PressedAttack: true,
	}})
	if len(created.Projectiles) != 1 {
		t.Fatalf("created projectiles = %d, want 1", len(created.Projectiles))
	}

	var destroyed Snapshot
	for range 10 {
		destroyed = state.Step(nil)
		if len(destroyed.Projectiles) == 1 && destroyed.Projectiles[0].IsDestroyed {
			break
		}
	}
	if len(destroyed.Projectiles) != 1 || !destroyed.Projectiles[0].IsDestroyed {
		t.Fatalf("projectile was not destroyed in the setup window: tick=%d projectiles=%+v", destroyed.Tick, destroyed.Projectiles)
	}
	destroyedID := destroyed.Projectiles[0].ID
	destroyedTick := destroyed.Tick

	for offset := Tick(0); offset < 30; offset++ {
		snapshot := destroyed
		if offset > 0 {
			snapshot = state.Step(nil)
		}
		if snapshot.Tick != destroyedTick+offset {
			t.Fatalf("retained snapshot tick=%d, want %d", snapshot.Tick, destroyedTick+offset)
		}
		if len(snapshot.Projectiles) != 1 || snapshot.Projectiles[0].ID != destroyedID || !snapshot.Projectiles[0].IsDestroyed {
			t.Fatalf("tombstone at D+%d = %+v, want one destroyed projectile %q", offset, snapshot.Projectiles, destroyedID)
		}
	}

	expired := state.Step(nil)
	if expired.Tick != destroyedTick+30 {
		t.Fatalf("expiry snapshot tick=%d, want %d", expired.Tick, destroyedTick+30)
	}
	if len(expired.Projectiles) != 0 {
		t.Fatalf("expired snapshot projectiles=%+v, want empty", expired.Projectiles)
	}
	if len(state.projectiles) != 0 {
		t.Fatalf("canonical projectiles after expiry=%+v, want empty", state.projectiles)
	}
}

func TestProjectileHistoryStaysBoundedDuringLongRunningRepeatedFire(t *testing.T) {
	state := newProjectileRangeState(1, nil)
	state.gameConfig.Projectile.Types[0].Speed = 100
	for index := range state.gameConfig.Player.Types {
		if state.gameConfig.Player.Types[index].CharacterType != CharacterTypeShelly {
			continue
		}
		state.gameConfig.Player.Types[index].NormalAttack.MaxCharges = 1
		state.gameConfig.Player.Types[index].NormalAttack.RechargeTicks = 1
	}
	state.attackStates["owner"] = attackState{charges: 1}

	const expectedMaximumProjectiles = 31
	maximumProjectiles := 0
	for range 300 {
		snapshot := state.Step([]InputCommand{{
			PlayerID:      "owner",
			AttackDir:     Vector2{X: 1},
			PressedAttack: true,
		}})
		if len(state.projectiles) > expectedMaximumProjectiles || len(snapshot.Projectiles) > expectedMaximumProjectiles {
			t.Fatalf("projectile history grew beyond bound: canonical=%d snapshot=%d", len(state.projectiles), len(snapshot.Projectiles))
		}
		if len(state.projectileDestroyedAt) > 30 {
			t.Fatalf("destroyed tick metadata grew beyond retention bound: %d", len(state.projectileDestroyedAt))
		}
		if len(state.projectiles) > maximumProjectiles {
			maximumProjectiles = len(state.projectiles)
		}
		if len(snapshot.Projectiles) != len(state.projectiles) {
			t.Fatalf("snapshot/canonical projectile count diverged: snapshot=%d canonical=%d", len(snapshot.Projectiles), len(state.projectiles))
		}

		canonicalByID := make(map[ProjectileID]ProjectileData, len(state.projectiles))
		for _, projectile := range state.projectiles {
			canonicalByID[projectile.ID] = projectile
		}
		seen := make(map[ProjectileID]ProjectileData, len(snapshot.Projectiles))
		for _, projectile := range snapshot.Projectiles {
			if _, exists := seen[projectile.ID]; exists {
				t.Fatalf("duplicate projectile ID %q in snapshot tick %d", projectile.ID, snapshot.Tick)
			}
			seen[projectile.ID] = projectile
		}
		for id, canonical := range canonicalByID {
			visible, ok := seen[id]
			if !ok {
				t.Fatalf("canonical projectile %q missing from snapshot tick %d", id, snapshot.Tick)
			}
			if visible.IsDestroyed != canonical.IsDestroyed {
				t.Fatalf("projectile %q destroyed state snapshot=%t canonical=%t at tick %d", id, visible.IsDestroyed, canonical.IsDestroyed, snapshot.Tick)
			}
		}
	}
	if maximumProjectiles != expectedMaximumProjectiles {
		t.Fatalf("steady-state projectile bound=%d, want %d", maximumProjectiles, expectedMaximumProjectiles)
	}
}
