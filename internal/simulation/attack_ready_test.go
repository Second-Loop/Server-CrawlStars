package simulation

import "testing"

func TestAttackReadyTickProjectsBurstLockAndUnlock(t *testing.T) {
	for _, skill := range []bool{false, true} {
		state := newColtSkillBurstState([]PlayerData{{ID: "colt", Team: TeamRed, CharacterType: CharacterTypeColt}})
		first := state.Step([]InputCommand{{PlayerID: "colt", AttackDir: Vector2{X: 1}, PressedAttack: !skill, PressedSkill: skill}})
		ready := Tick(17)
		if skill {
			ready = 18
		}
		if got := playerByID(t, first, "colt").AttackReadyTick; got != ready {
			t.Fatalf("skill=%t ready=%d want=%d", skill, got, ready)
		}
		for tick := Tick(2); tick < ready; tick++ {
			snap := state.Step([]InputCommand{{PlayerID: "colt", AttackDir: Vector2{X: 1}, PressedAttack: true}})
			player := playerByID(t, snap, "colt")
			if player.PressedAttack {
				t.Fatalf("attack approved during burst at %d", tick)
			}
			if tick < ready-1 && player.AttackReadyTick != ready {
				t.Fatalf("lock lost at %d", tick)
			}
			if tick == ready-1 && player.AttackReadyTick != 0 {
				t.Fatalf("completed burst retains lock: %+v", player)
			}
		}
		snap := state.Step([]InputCommand{{PlayerID: "colt", AttackDir: Vector2{X: 1}, PressedAttack: true}})
		if !playerByID(t, snap, "colt").PressedAttack {
			t.Fatal("inclusive ready tick rejects attack")
		}
	}
}
