package simulation

import (
	"reflect"
	"testing"
)

func TestColtSkillBurstEmitsExactScheduleFromCurrentPositionWithFixedDirection(t *testing.T) {
	state := newColtSkillBurstState([]PlayerData{{
		ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt,
	}})
	dueTicks := map[Tick]bool{1: true, 3: true, 5: true, 7: true, 8: true, 10: true, 12: true, 14: true, 15: true, 17: true, 19: true, 21: true}
	seen := make(map[ProjectileID]bool)

	for inputTick := Tick(1); inputTick <= 22; inputTick++ {
		attackDirection := Vector2{Y: 1}
		if inputTick == 1 {
			attackDirection = Vector2{X: 1}
		}
		snapshot := state.Step([]InputCommand{{
			PlayerID: "colt", ClientTick: int64(inputTick), MoveDir: Vector2{Y: 1},
			AttackDir: attackDirection, PressedSkill: inputTick == 1,
		}})

		player := playerByID(t, snapshot, "colt")
		if player.PressedSkill != (inputTick == 1) {
			t.Fatalf("tick %d PressedSkill=%t, want %t", inputTick, player.PressedSkill, inputTick == 1)
		}
		newProjectiles := make([]ProjectileData, 0, 1)
		for _, projectile := range snapshot.Projectiles {
			if !seen[projectile.ID] {
				seen[projectile.ID] = true
				newProjectiles = append(newProjectiles, projectile)
			}
		}
		wantNew := 0
		if dueTicks[inputTick] {
			wantNew = 1
		}
		if len(newProjectiles) != wantNew {
			t.Fatalf("tick %d new projectiles=%d, want %d", inputTick, len(newProjectiles), wantNew)
		}
		if wantNew == 0 {
			continue
		}
		projectile := newProjectiles[0]
		assertVector(t, "Colt skill projectile position", projectile.Pos, player.Pos)
		assertVector(t, "Colt skill fixed direction", projectile.Dir, Vector2{X: 1})
		if projectile.Type != "colt_skill" || projectile.Damage != 320 || projectile.Speed != 13 || projectile.Radius != 0.3 {
			t.Fatalf("tick %d projectile=%+v, want config-owned Colt skill stats", inputTick, projectile)
		}
		if runtime := state.projectileRuntime[projectile.ID]; runtime.maxDistance != 11*TileSize {
			t.Fatalf("tick %d max distance=%v, want %v", inputTick, runtime.maxDistance, 11*TileSize)
		}
	}

	if got := len(seen); got != 12 {
		t.Fatalf("skill projectile count=%d, want 12", got)
	}
	player := playerByID(t, state.Step(nil), "colt")
	if player.SkillReadyTick != 391 || player.AttackCharges != 3 {
		t.Fatalf("skill state=%+v, want ready tick 391 and unchanged 3 charges", player)
	}
}

func TestColtSkillCancelsFutureNormalBurstButKeepsSameTickCommittedEmission(t *testing.T) {
	state := newColtSkillBurstState([]PlayerData{{
		ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt,
	}})
	state.Step([]InputCommand{{PlayerID: "colt", AttackDir: Vector2{X: 1}, PressedAttack: true}})
	state.Step(nil)
	state.Step(nil)

	snapshot := state.Step([]InputCommand{{
		PlayerID: "colt", ClientTick: 1, AttackDir: Vector2{Y: 1}, PressedSkill: true,
	}})
	if !playerByID(t, snapshot, "colt").PressedSkill {
		t.Fatal("Colt skill was not approved")
	}
	if got := len(snapshot.Projectiles); got != 3 {
		t.Fatalf("projectiles on same-tick normal due + skill approval=%d, want 3", got)
	}
	if snapshot.Projectiles[1].Type != "default" || snapshot.Projectiles[2].Type != "colt_skill" {
		t.Fatalf("same-tick committed order=%v/%v, want default then colt_skill", snapshot.Projectiles[1].Type, snapshot.Projectiles[2].Type)
	}

	for snapshot.Tick < 7 {
		snapshot = state.Step(nil)
	}
	defaultCount := 0
	for _, projectile := range snapshot.Projectiles {
		if projectile.Type == "default" {
			defaultCount++
		}
	}
	if defaultCount != 2 {
		t.Fatalf("default projectiles after cancellation=%d, want activation and already-committed due emission only", defaultCount)
	}
}

func TestColtSkillLocksNormalAttackThroughLastEmissionWithoutConsumingCharge(t *testing.T) {
	state := newColtSkillBurstState([]PlayerData{{
		ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt,
	}})
	state.attackStates["colt"] = attackState{charges: 2}
	state.Step([]InputCommand{{PlayerID: "colt", ClientTick: 1, AttackDir: Vector2{X: 1}, PressedSkill: true}})

	for inputTick := Tick(2); inputTick <= 21; inputTick++ {
		snapshot := state.Step([]InputCommand{{
			PlayerID: "colt", ClientTick: int64(inputTick), AttackDir: Vector2{Y: 1}, PressedAttack: true,
		}})
		if playerByID(t, snapshot, "colt").PressedAttack {
			t.Fatalf("normal attack approved while skill burst active at tick %d", inputTick)
		}
		if got := state.attackStates["colt"].charges; got != 2 {
			t.Fatalf("tick %d charges=%d, want unchanged 2", inputTick, got)
		}
	}

	accepted := state.Step([]InputCommand{{
		PlayerID: "colt", ClientTick: 22, AttackDir: Vector2{Y: 1}, PressedAttack: true,
	}})
	if !playerByID(t, accepted, "colt").PressedAttack {
		t.Fatal("normal attack was not accepted on tick after final skill emission")
	}
	if got := state.attackStates["colt"].charges; got != 1 {
		t.Fatalf("charges after accepted normal attack=%d, want 1", got)
	}
	if got := accepted.Projectiles[len(accepted.Projectiles)-1].Type; got != "default" {
		t.Fatalf("post-skill projectile type=%q, want default", got)
	}
}

func TestColtSkillCooldownReinputDoesNotReplaceOrQueueBurst(t *testing.T) {
	state := newColtSkillBurstState([]PlayerData{{
		ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt,
	}})
	state.Step([]InputCommand{{
		PlayerID: "colt", ClientTick: 1, AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})

	blocked := state.Step([]InputCommand{{
		PlayerID: "colt", ClientTick: 2, AttackDir: Vector2{Y: 1}, PressedAttack: true, PressedSkill: true,
	}})
	player := playerByID(t, blocked, "colt")
	if player.PressedSkill || player.PressedAttack || player.SkillReadyTick != 391 || player.LastProcessedClientTick != 2 {
		t.Fatalf("cooldown reinput result=%+v, want ACK only with unchanged cooldown", player)
	}
	if got := len(blocked.Projectiles); got != 1 {
		t.Fatalf("cooldown reinput projectile count=%d, want 1", got)
	}

	due := state.Step(nil)
	if got := len(due.Projectiles); got != 2 {
		t.Fatalf("original burst due count=%d, want 2", got)
	}
	assertVector(t, "cooldown reinput must not replace direction", due.Projectiles[1].Dir, Vector2{X: 1})
}

func TestColtSkillOwnerDeathCancelsFutureEmissionsButKeepsCommittedProjectiles(t *testing.T) {
	state := newColtSkillBurstState([]PlayerData{
		{ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt, HP: 100},
		{ID: "enemy", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 100}},
	})
	activated := state.Step([]InputCommand{{
		PlayerID: "colt", AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})
	if len(activated.Projectiles) != 1 {
		t.Fatalf("activation projectiles=%d, want 1", len(activated.Projectiles))
	}
	firstID := activated.Projectiles[0].ID
	state.Step(nil)
	state.projectiles = append(state.projectiles, ProjectileData{
		ID: "killer", OwnerID: "enemy", Pos: Vector2{}, Damage: 100, Radius: 0.3,
	})

	death := state.Step(nil)
	if !playerByID(t, death, "colt").IsDead {
		t.Fatal("Colt was not killed in projectile pre-phase")
	}
	if got := len(death.Projectiles); got != 2 {
		t.Fatalf("projectiles after pre-phase death=%d, want first committed skill projectile and killer", got)
	}
	if death.Projectiles[0].ID != firstID {
		t.Fatalf("committed projectile %q was not retained: %+v", firstID, death.Projectiles)
	}
	if _, active := state.burstStates["colt"]; active {
		t.Fatal("dead Colt skill burst remains active")
	}
}

func TestColtSkillApprovalSurvivesSameTickMeleeDeathThenCancelsFutureBurst(t *testing.T) {
	state := newColtSkillBurstState([]PlayerData{
		{ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt, HP: 1100},
		{ID: "lily", Team: TeamBlue, CharacterType: CharacterTypeLily, Pos: Vector2{X: -2}},
	})

	terminal := state.Step([]InputCommand{
		{PlayerID: "colt", AttackDir: Vector2{Y: 1}, PressedSkill: true},
		lilyAttackInput("lily", Vector2{X: 1}),
	})
	colt := playerByID(t, terminal, "colt")
	if !colt.PressedSkill || !colt.IsDead || colt.HP != 0 {
		t.Fatalf("same-tick approval/death Colt=%+v, want approved and dead", colt)
	}
	if got := len(terminal.Projectiles); got != 1 || terminal.Projectiles[0].Type != "colt_skill" {
		t.Fatalf("same-tick committed skill projectiles=%+v, want one colt_skill", terminal.Projectiles)
	}
	state.Step(nil)
	afterDue := state.Step(nil)
	if got := len(afterDue.Projectiles); got != 1 {
		t.Fatalf("projectiles after dead-owner future due tick=%d, want 1", got)
	}
	if _, active := state.burstStates["colt"]; active {
		t.Fatal("same-tick melee death left Colt burst active")
	}
}

func TestColtSkillProjectileUsesRangeAndModeHitRules(t *testing.T) {
	for _, tt := range []struct {
		name   string
		team   Team
		wantHP float64
	}{
		{name: "enemy endpoint tangent hit", team: TeamBlue, wantHP: DefaultPlayerHP - 320},
		{name: "ally passes through", team: TeamRed, wantHP: DefaultPlayerHP},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := newColtSkillBurstState([]PlayerData{
				{ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt},
				{ID: "target", Team: tt.team, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 11*TileSize + DefaultProjectileRadius + DefaultPlayerRadius}},
			})
			state.Step([]InputCommand{{PlayerID: "colt", AttackDir: Vector2{X: 1}, PressedSkill: true}})
			state.EliminatePlayers([]PlayerID{"colt"})

			var snapshot Snapshot
			for range 31 {
				snapshot = state.Step(nil)
			}
			assertPlayerHP(t, snapshot, "target", tt.wantHP, false)
		})
	}
}

func TestColtSkillProjectileUsesExistingWallCollision(t *testing.T) {
	gameConfig := StaticGameConfig()
	gameConfig.Map = lineMapWithTile(3, TileWall)
	state := NewStateWithConfig([]PlayerData{
		{ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt, Pos: Vector2{X: -1.5}},
		{ID: "target", Team: TeamBlue, CharacterType: CharacterTypeShelly, Pos: Vector2{X: 1.5}},
	}, Config{Game: gameConfig})
	state.Step([]InputCommand{{PlayerID: "colt", AttackDir: Vector2{X: 1}, PressedSkill: true}})
	state.Step(nil)
	snapshot := state.Step(nil)

	if len(snapshot.Projectiles) != 2 || !snapshot.Projectiles[0].IsDestroyed || snapshot.Projectiles[1].IsDestroyed {
		t.Fatalf("wall collision projectiles=%+v, want first destroyed and newly due second alive", snapshot.Projectiles)
	}
	assertPlayerHP(t, snapshot, "target", DefaultPlayerHP, false)
}

func TestColtSkillEmissionIDsAreIndependentOfInputOrder(t *testing.T) {
	players := []PlayerData{
		{ID: "colt-b", Team: TeamRed, CharacterType: CharacterTypeColt, Pos: Vector2{X: 100}},
		{ID: "colt-a", Team: TeamBlue, CharacterType: CharacterTypeColt, Pos: Vector2{X: -100}},
	}
	inputs := []InputCommand{
		{PlayerID: "colt-b", ClientTick: 1, AttackDir: Vector2{X: -1}, PressedSkill: true},
		{PlayerID: "colt-a", ClientTick: 1, AttackDir: Vector2{X: 1}, PressedSkill: true},
	}
	state := newColtSkillBurstState(players)
	reversedState := newColtSkillBurstState(players)

	for tick := Tick(1); tick <= 21; tick++ {
		forwardInputs := []InputCommand(nil)
		reversedInputs := []InputCommand(nil)
		if tick == 1 {
			forwardInputs = inputs
			reversedInputs = []InputCommand{inputs[1], inputs[0]}
		}
		forward := state.Step(forwardInputs)
		reversed := reversedState.Step(reversedInputs)
		if !reflect.DeepEqual(forward, reversed) {
			t.Fatalf("tick %d snapshot differs by input order:\nforward=%+v\nreversed=%+v", tick, forward, reversed)
		}
	}
}

func newColtSkillBurstState(players []PlayerData) *State {
	gameConfig := StaticGameConfig()
	gameConfig.Map = MapData{}
	return NewStateWithConfig(players, Config{Game: gameConfig})
}
