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

// dispatchApprovedSkill is the typed handoff used by the character effect
// tickets. SL-120 intentionally keeps every branch effect-free.
func (s *State) dispatchApprovedSkill(_ int, _ Vector2, skill SkillConfig) {
	switch skill.Kind {
	case SkillReloadDash:
		_ = skill.ReloadDash
	case SkillBurstProjectile:
		_ = skill.BurstProjectile
	case SkillTeleportProjectile:
		_ = skill.TeleportProjectile
	}
}
