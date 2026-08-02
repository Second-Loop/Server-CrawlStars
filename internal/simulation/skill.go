package simulation

func (s *State) tryApproveSkill(playerIndex int, activationTick Tick) bool {
	player := &s.players[playerIndex]
	if activationTick < player.SkillReadyTick {
		return false
	}
	playerType, ok := s.gameConfig.PlayerType(player.CharacterType)
	if !ok || playerType.Skill.CooldownTicks <= 0 {
		return false
	}
	player.PressedSkill = true
	player.SkillReadyTick = activationTick + Tick(playerType.Skill.CooldownTicks)
	return true
}
