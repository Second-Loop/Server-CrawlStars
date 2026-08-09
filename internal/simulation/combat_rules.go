package simulation

func CanPlayerDamage(owner PlayerData, target PlayerData, mode GameModeConfig) bool {
	if owner.ID == target.ID || target.IsDead {
		return false
	}
	if mode.Rules.TeamBehavior == TeamBehaviorFreeForAll {
		return true
	}
	return mode.Rules.TeamBehavior == TeamBehaviorTwoTeams &&
		(mode.Rules.FriendlyFire || owner.Team != target.Team)
}
