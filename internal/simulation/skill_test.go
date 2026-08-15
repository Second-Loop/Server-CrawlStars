package simulation

import (
	"math"
	"reflect"
	"testing"
)

func TestStepSkillCooldownAllowsInclusiveReadyTick(t *testing.T) {
	state := newSkillTestState(t, CharacterTypeShelly, 2)

	first := state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 1,
		AttackDir: Vector2{Y: 1}, PressedSkill: true,
	}})
	assertSkillState(t, first, "player", true, 3, 1)

	second := state.Step(nil)
	assertSkillState(t, second, "player", false, 3, 1)

	third := state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 2,
		AttackDir: Vector2{Y: 1}, PressedSkill: true,
	}})
	assertSkillState(t, third, "player", true, 5, 2)
}

func TestTryApproveSkillReturnsTypedCharacterConfig(t *testing.T) {
	state := newSkillTestState(t, CharacterTypeColt, 390)

	skill, approved := state.tryApproveSkill(0, 1)
	if !approved || skill.Kind != SkillBurstProjectile || skill.BurstProjectile == nil {
		t.Fatalf("tryApproveSkill()=(%+v,%t), want typed Colt burst config", skill, approved)
	}
	if got, want := skill.BurstProjectile.Projectile.EmissionOffsetsTicks, []int{0, 2, 4, 6, 7, 9, 11, 13, 14, 16, 18, 20}; !reflect.DeepEqual(got, want) {
		t.Fatalf("approved skill offsets=%v, want %v", got, want)
	}
}

func TestStepDoesNotQueueCooldownBlockedSkill(t *testing.T) {
	state := newSkillTestState(t, CharacterTypeShelly, 3)

	approved := state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 1,
		AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})
	assertSkillState(t, approved, "player", true, 4, 1)

	for clientTick := int64(2); clientTick <= 3; clientTick++ {
		blocked := state.Step([]InputCommand{{
			PlayerID: "player", ClientTick: clientTick,
			AttackDir: Vector2{X: 1}, PressedSkill: true,
		}})
		assertSkillState(t, blocked, "player", false, 4, clientTick)
	}

	readyWithoutCommand := state.Step(nil)
	assertSkillState(t, readyWithoutCommand, "player", false, 4, 3)

	newCommand := state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 5,
		AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})
	assertSkillState(t, newCommand, "player", true, 8, 5)
}

func TestStepSkillPriorityOverNormalAttack(t *testing.T) {
	tests := []struct {
		name        string
		readyTick   Tick
		charges     int
		wantSkill   bool
		wantAttack  bool
		wantCharges int
	}{
		{"ready_with_charge", 0, 1, true, false, 3},
		{"cooldown_with_charge", 2, 1, false, true, 0},
		{"ready_without_charge", 0, 0, true, false, 3},
		{"cooldown_without_charge", 2, 0, false, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newSkillTestState(t, CharacterTypeShelly, 2)
			state.attackStates["player"] = attackState{charges: tt.charges}
			state.players[0].SkillReadyTick = tt.readyTick

			snapshot := state.Step([]InputCommand{{
				PlayerID: "player", ClientTick: 1,
				AttackDir: Vector2{X: 1}, PressedAttack: true, PressedSkill: true,
			}})
			player := playerByID(t, snapshot, "player")

			if player.PressedSkill != tt.wantSkill {
				t.Errorf("PressedSkill = %t, want %t", player.PressedSkill, tt.wantSkill)
			}
			if player.PressedAttack != tt.wantAttack {
				t.Errorf("PressedAttack = %t, want %t", player.PressedAttack, tt.wantAttack)
			}
			if got := state.attackStates["player"].charges; got != tt.wantCharges {
				t.Errorf("charges = %d, want %d", got, tt.wantCharges)
			}
			if tt.wantSkill && len(snapshot.Projectiles) != 0 {
				t.Errorf("skill-winning Shelly command created %d projectiles, want 0", len(snapshot.Projectiles))
			}
		})
	}
}

func TestStepInitialSkillStateIsFalseAndReadyZero(t *testing.T) {
	state := newSkillTestState(t, CharacterTypeShelly, 2)

	snapshot := state.Step(nil)

	assertSkillState(t, snapshot, "player", false, 0, 0)
}

func TestStepCooldownBlockedSkillAcknowledgesValidClientTick(t *testing.T) {
	state := newSkillTestState(t, CharacterTypeShelly, 3)
	state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 1,
		AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})

	blocked := state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 2,
		AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})

	assertSkillState(t, blocked, "player", false, 4, 2)
}

func TestStepInvalidSkillInputPreservesStateAndACK(t *testing.T) {
	tests := []struct {
		name    string
		command InputCommand
		prepare func(*State)
	}{
		{
			name: "NaN direction",
			command: InputCommand{
				PlayerID: "player", ClientTick: 8,
				AttackDir: Vector2{X: math.NaN()}, PressedAttack: true, PressedSkill: true,
			},
		},
		{
			name: "dead player",
			command: InputCommand{
				PlayerID: "player", ClientTick: 8,
				AttackDir: Vector2{X: 1}, PressedAttack: true, PressedSkill: true,
			},
			prepare: func(state *State) { state.players[0].IsDead = true },
		},
		{
			name: "negative tick",
			command: InputCommand{
				PlayerID: "player", ClientTick: -1,
				AttackDir: Vector2{X: 1}, PressedAttack: true, PressedSkill: true,
			},
		},
		{
			name: "stale tick",
			command: InputCommand{
				PlayerID: "player", ClientTick: 7,
				AttackDir: Vector2{X: 1}, PressedAttack: true, PressedSkill: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newSkillTestState(t, CharacterTypeShelly, 2)
			state.players[0].SkillReadyTick = 9
			state.players[0].LastProcessedClientTick = 7
			state.attackStates["player"] = attackState{charges: 1}
			if tt.prepare != nil {
				tt.prepare(state)
			}

			snapshot := state.Step([]InputCommand{tt.command})
			player := playerByID(t, snapshot, "player")

			if player.PressedSkill || player.PressedAttack {
				t.Errorf("invalid input approved action: skill=%t attack=%t", player.PressedSkill, player.PressedAttack)
			}
			if player.SkillReadyTick != 9 {
				t.Errorf("SkillReadyTick = %d, want 9", player.SkillReadyTick)
			}
			if player.LastProcessedClientTick != 7 {
				t.Errorf("ACK = %d, want 7", player.LastProcessedClientTick)
			}
			if got := state.attackStates["player"].charges; got != 1 {
				t.Errorf("charges = %d, want 1", got)
			}
		})
	}
}

func TestStepZeroAttackDirectionDoesNotApproveSkill(t *testing.T) {
	state := newSkillTestState(t, CharacterTypeShelly, 2)
	state.attackStates["player"] = attackState{charges: 1}

	snapshot := state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 1,
		PressedAttack: true, PressedSkill: true,
	}})
	player := playerByID(t, snapshot, "player")

	if player.PressedSkill || player.PressedAttack {
		t.Errorf("zero direction approved action: skill=%t attack=%t", player.PressedSkill, player.PressedAttack)
	}
	if player.SkillReadyTick != 0 {
		t.Errorf("SkillReadyTick = %d, want 0", player.SkillReadyTick)
	}
	if got := state.attackStates["player"].charges; got != 1 {
		t.Errorf("charges = %d, want 1", got)
	}
}

func TestStepSkillResultIsIndependentOfInputOrder(t *testing.T) {
	gameConfig := skillTestGameConfig(t, CharacterTypeShelly, 2)
	players := []PlayerData{
		{ID: "player-b", Team: TeamRed, CharacterType: CharacterTypeShelly},
		{ID: "player-a", Team: TeamBlue, CharacterType: CharacterTypeShelly},
	}
	inputs := []InputCommand{
		{PlayerID: "player-b", ClientTick: 1, AttackDir: Vector2{X: -1}, PressedSkill: true},
		{PlayerID: "player-a", ClientTick: 1, AttackDir: Vector2{X: 1}, PressedSkill: true},
	}

	state := NewStateWithConfig(players, Config{Game: gameConfig})
	reversedState := NewStateWithConfig(players, Config{Game: gameConfig})
	forward := state.Step(inputs)
	reversed := reversedState.Step([]InputCommand{inputs[1], inputs[0]})

	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("skill snapshots differ by input order:\nforward: %+v\nreversed: %+v", forward, reversed)
	}
}

func TestStepSkillDoesNotCancelExistingColtBurst(t *testing.T) {
	state := newSkillTestState(t, CharacterTypeColt, 2)
	activation := state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 1,
		AttackDir: Vector2{X: 1}, PressedAttack: true,
	}})
	if len(activation.Projectiles) != 1 {
		t.Fatalf("activation projectiles = %d, want 1", len(activation.Projectiles))
	}

	skill := state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 2,
		AttackDir: Vector2{Y: 1}, PressedSkill: true,
	}})
	if !playerByID(t, skill, "player").PressedSkill {
		t.Fatal("skill command was not approved during Colt burst")
	}

	var due Snapshot
	for tick := Tick(3); tick <= 7; tick++ {
		due = state.Step(nil)
	}
	if due.Tick != 7 {
		t.Fatalf("due snapshot tick = %d, want 7", due.Tick)
	}
	if len(due.Projectiles) != 3 {
		t.Fatalf("projectiles at activation tick A+6 = %d, want 3", len(due.Projectiles))
	}
}

func TestStepMissingOrFalsePressedSkillUsesExistingAttackPath(t *testing.T) {
	tests := []struct {
		name  string
		input InputCommand
	}{
		{
			name: "omitted",
			input: InputCommand{
				PlayerID: "player", ClientTick: 1,
				AttackDir: Vector2{X: 1}, PressedAttack: true,
			},
		},
		{
			name: "false",
			input: InputCommand{
				PlayerID: "player", ClientTick: 1,
				AttackDir: Vector2{X: 1}, PressedAttack: true, PressedSkill: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newSkillTestState(t, CharacterTypeShelly, 2)
			beforeCharges := state.attackStates["player"].charges

			snapshot := state.Step([]InputCommand{tt.input})
			player := playerByID(t, snapshot, "player")

			if !player.PressedAttack || player.PressedSkill {
				t.Errorf("attack path flags: skill=%t attack=%t", player.PressedSkill, player.PressedAttack)
			}
			if got := state.attackStates["player"].charges; got != beforeCharges-1 {
				t.Errorf("charges = %d, want %d", got, beforeCharges-1)
			}
			if len(snapshot.Projectiles) == 0 {
				t.Fatal("normal attack path created no projectiles")
			}
		})
	}
}

func TestSkillSnapshotDoesNotExposeMutableState(t *testing.T) {
	state := newSkillTestState(t, CharacterTypeShelly, 2)
	first := state.Step([]InputCommand{{
		PlayerID: "player", ClientTick: 1,
		AttackDir: Vector2{X: 1}, PressedSkill: true,
	}})
	first.Players[0].SkillReadyTick = 999

	second := state.Step(nil)

	assertSkillState(t, second, "player", false, 3, 1)
}

func skillTestGameConfig(t *testing.T, characterType CharacterType, cooldownTicks int) GameConfig {
	t.Helper()

	gameConfig := StaticGameConfig()
	found := false
	for i := range gameConfig.Player.Types {
		if gameConfig.Player.Types[i].CharacterType != characterType {
			continue
		}
		gameConfig.Player.Types[i].Skill.CooldownTicks = cooldownTicks
		found = true
		break
	}
	if !found {
		t.Fatalf("character type %d not found in static game config", characterType)
	}
	resolved, err := ResolveGameConfig(gameConfig)
	if err != nil {
		t.Fatalf("ResolveGameConfig() error = %v", err)
	}
	return resolved
}

func newSkillTestState(t *testing.T, characterType CharacterType, cooldownTicks int) *State {
	t.Helper()

	return NewStateWithConfig([]PlayerData{{
		ID:            "player",
		Team:          TeamRed,
		CharacterType: characterType,
	}}, Config{Game: skillTestGameConfig(t, characterType, cooldownTicks)})
}

func assertSkillState(t *testing.T, snapshot Snapshot, playerID PlayerID, pressedSkill bool, readyTick Tick, ack int64) {
	t.Helper()

	player := playerByID(t, snapshot, playerID)
	if player.PressedSkill != pressedSkill {
		t.Errorf("PressedSkill = %t, want %t", player.PressedSkill, pressedSkill)
	}
	if player.SkillReadyTick != readyTick {
		t.Errorf("SkillReadyTick = %d, want %d", player.SkillReadyTick, readyTick)
	}
	if player.LastProcessedClientTick != ack {
		t.Errorf("ACK = %d, want %d", player.LastProcessedClientTick, ack)
	}
}
