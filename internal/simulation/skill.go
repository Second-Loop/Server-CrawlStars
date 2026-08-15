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

// dispatchApprovedSkill applies immediate character state and returns movement
// that must be resolved with the other same-tick skill effects as one batch.
func (s *State) dispatchApprovedSkill(playerIndex int, direction Vector2, skill SkillConfig) (skillDashIntent, bool) {
	switch skill.Kind {
	case SkillReloadDash:
		if skill.ReloadDash == nil || playerIndex < 0 || playerIndex >= len(s.players) {
			return skillDashIntent{}, false
		}
		playerID := s.players[playerIndex].ID
		attack, ok := s.normalAttackConfig(playerID)
		if !ok {
			return skillDashIntent{}, false
		}
		s.attackStates[playerID] = attackState{charges: attack.MaxCharges}
		return skillDashIntent{
			playerIndex: playerIndex,
			direction:   direction,
			distance:    skill.ReloadDash.DashDistanceTiles * s.resolvedTileSize(),
		}, true
	case SkillBurstProjectile:
		_ = skill.BurstProjectile
	case SkillTeleportProjectile:
		_ = skill.TeleportProjectile
	}
	return skillDashIntent{}, false
}
