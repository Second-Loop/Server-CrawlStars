package simulation

// timedDashState owns the fixed direction and one swept segment per server tick.
type timedDashState struct {
	direction      Vector2
	stepDistance   float64
	remainingTicks int
	readyTick      Tick
}

// Approve only reload dashes before movement, keeping projectile/teleport skill
// approval after ordinary movement so their existing emission origins stay intact.
func (s *State) prepareTimedDashes(inputs []preparedInput, tick Tick) {
	for i := range inputs {
		input := &inputs[i]
		player := &s.players[input.playerIndex]
		if _, active := s.dashStates[player.ID]; !active && input.input.PressedSkill && input.attackDir != (Vector2{}) {
			if playerType, ok := s.gameConfig.PlayerType(player.CharacterType); ok && playerType.Skill.Kind == SkillReloadDash {
				if skill, approved := s.tryApproveSkill(input.playerIndex, tick); approved {
					s.dispatchApprovedSkill(input.playerIndex, input.attackDir, skill, tick)
				}
			}
		}
		if dash, active := s.dashStates[player.ID]; active {
			input.movement = Vector2{}
			player.MoveDir = Vector2{}
			player.AttackDir = dash.direction
		}
	}
}

func (s *State) stepTimedDashes() {
	intents := make([]skillDashIntent, 0, len(s.dashStates))
	for i, player := range s.players {
		if player.IsDead {
			s.clearPlayerDash(i)
			continue
		}
		if dash, active := s.dashStates[player.ID]; active {
			intents = append(intents, skillDashIntent{playerIndex: i, direction: dash.direction, distance: dash.stepDistance})
		}
	}
	collisions := s.applySkillDashes(intents)
	for _, intent := range intents {
		playerID := s.players[intent.playerIndex].ID
		dash := s.dashStates[playerID]
		dash.remainingTicks--
		if dash.remainingTicks == 0 || collisions[playerID] {
			s.clearPlayerDash(intent.playerIndex)
		} else {
			s.dashStates[playerID] = dash
		}
	}
}

func (s *State) clearPlayerDash(index int) {
	delete(s.dashStates, s.players[index].ID)
	s.players[index].IsDashing = false
	s.players[index].AttackReadyTick = 0
}
