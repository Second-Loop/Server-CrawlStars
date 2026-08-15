package simulation

func (s *State) tryApproveSkill(playerIndex int, activationTick Tick) (SkillConfig, bool) {
	player := &s.players[playerIndex]
	if activationTick < player.SkillReadyTick {
		return SkillConfig{}, false
	}
	playerType, ok := s.gameConfig.PlayerType(player.CharacterType)
	if !ok || playerType.Skill.CooldownTicks <= 0 {
		return SkillConfig{}, false
	}
	player.PressedSkill = true
	player.SkillReadyTick = activationTick + Tick(playerType.Skill.CooldownTicks)
	return playerType.Skill, true
}

type approvedSkillEffect struct {
	dash      skillDashIntent
	hasDash   bool
	emissions []projectileEmission
}

// dispatchApprovedSkill applies immediate character state and returns effects
// that must be committed in the remaining same-tick simulation phases.
func (s *State) dispatchApprovedSkill(playerIndex int, direction Vector2, skill SkillConfig, activationTick Tick) approvedSkillEffect {
	switch skill.Kind {
	case SkillReloadDash:
		if skill.ReloadDash == nil || playerIndex < 0 || playerIndex >= len(s.players) {
			return approvedSkillEffect{}
		}
		playerID := s.players[playerIndex].ID
		attack, ok := s.normalAttackConfig(playerID)
		if !ok {
			return approvedSkillEffect{}
		}
		s.attackStates[playerID] = attackState{charges: attack.MaxCharges}
		return approvedSkillEffect{
			dash: skillDashIntent{
				playerIndex: playerIndex,
				direction:   direction,
				distance:    skill.ReloadDash.DashDistanceTiles * s.resolvedTileSize(),
			},
			hasDash: true,
		}
	case SkillBurstProjectile:
		if skill.BurstProjectile == nil || playerIndex < 0 || playerIndex >= len(s.players) {
			return approvedSkillEffect{}
		}
		playerID := s.players[playerIndex].ID
		attack := skillProjectileAttackSpec(*skill.BurstProjectile)
		s.burstStates[playerID] = burstState{
			direction:      direction,
			attack:         attack,
			activationTick: activationTick,
			nextOrdinal:    1,
		}
		emission, ok := s.newProjectileEmission(playerID, direction, attack, projectileEmissionActivation, 0, activationTick)
		if !ok {
			delete(s.burstStates, playerID)
			return approvedSkillEffect{}
		}
		return approvedSkillEffect{emissions: []projectileEmission{emission}}
	case SkillTeleportProjectile:
		if skill.TeleportProjectile == nil || playerIndex < 0 || playerIndex >= len(s.players) {
			return approvedSkillEffect{}
		}
		playerID := s.players[playerIndex].ID
		attack := skillTeleportProjectileAttackSpec(*skill.TeleportProjectile)
		emission, ok := s.newProjectileEmission(playerID, direction, attack, projectileEmissionActivation, 0, activationTick)
		if !ok {
			return approvedSkillEffect{}
		}
		return approvedSkillEffect{emissions: []projectileEmission{emission}}
	}
	return approvedSkillEffect{}
}
