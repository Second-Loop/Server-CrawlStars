package rooms

import "github.com/Second-Loop/Server-CrawlStars/internal/simulation"

type gameEndResult string

type gameEndCalculator func(simulation.GameConfig, simulation.Snapshot) map[string]gameEndResult

const (
	gameEndResultWin  gameEndResult = "Win"
	gameEndResultLose gameEndResult = "Lose"
	gameEndResultDraw gameEndResult = "Draw"
)

func (r gameEndResult) String() string {
	return string(r)
}

func (r *room) gameEndResults(snapshot simulation.Snapshot) map[string]gameEndResult {
	return r.calculateGameEnd(r.gameConfig, snapshot)
}

func calculateGameEndResults(gameConfig simulation.GameConfig, snapshot simulation.Snapshot) map[string]gameEndResult {
	switch gameConfig.SelectedMode.ID {
	case simulation.GameModeDuel1v1:
		return duelGameEndResults(snapshot.Players)
	default:
		// Non-duel mode rules are not active yet. Keep the current player-survival
		// result rule for debug/custom rooms until a mode-specific issue defines it.
		return playerSurvivalGameEndResults(snapshot.Players)
	}
}

func shouldEndGame(gameConfig simulation.GameConfig, snapshot simulation.Snapshot) bool {
	players := snapshot.Players
	if len(players) == 0 {
		return false
	}

	switch gameConfig.SelectedMode.ID {
	case simulation.GameModeDuel1v1:
		return len(duelGameEndResults(players)) > 0
	case simulation.GameModeSolo:
		alive := livePlayerCount(players)
		return alive < len(players) && alive <= 1
	case simulation.GameModeTeam:
		for _, eliminated := range configuredTeamEliminations(gameConfig.SelectedMode, players) {
			if eliminated {
				return true
			}
		}
		return false
	default:
		return len(playerSurvivalGameEndResults(players)) > 0
	}
}

func duelGameEndResults(players []simulation.PlayerData) map[string]gameEndResult {
	return playerSurvivalGameEndResults(players)
}

func soloGameEndResults(players []simulation.PlayerData) map[string]gameEndResult {
	if len(players) == 0 {
		return nil
	}

	alive := livePlayerCount(players)
	if alive == len(players) {
		return nil
	}

	results := make(map[string]gameEndResult, len(players))
	if alive == 0 {
		for _, player := range players {
			results[string(player.ID)] = gameEndResultDraw
		}
		return results
	}

	for _, player := range players {
		if player.IsDead {
			results[string(player.ID)] = gameEndResultLose
			continue
		}
		if alive == 1 {
			results[string(player.ID)] = gameEndResultWin
		}
	}
	return results
}

func teamGameEndResults(
	mode simulation.GameModeConfig,
	players []simulation.PlayerData,
) map[string]gameEndResult {
	if len(players) == 0 {
		return nil
	}

	eliminated := configuredTeamEliminations(mode, players)
	eliminatedCount := 0
	for _, teamEliminated := range eliminated {
		if teamEliminated {
			eliminatedCount++
		}
	}
	if eliminatedCount == 0 {
		return nil
	}

	results := make(map[string]gameEndResult, len(players))
	if eliminatedCount == len(eliminated) {
		for _, player := range players {
			results[string(player.ID)] = gameEndResultDraw
		}
		return results
	}

	for _, player := range players {
		teamEliminated, configured := eliminated[player.Team]
		if !configured {
			continue
		}
		if teamEliminated {
			results[string(player.ID)] = gameEndResultLose
		} else {
			results[string(player.ID)] = gameEndResultWin
		}
	}
	return results
}

func configuredTeamEliminations(
	mode simulation.GameModeConfig,
	players []simulation.PlayerData,
) map[simulation.Team]bool {
	if len(players) == 0 {
		return nil
	}

	liveByTeam := make(map[simulation.Team]int, len(mode.Teams))
	for _, player := range players {
		if !player.IsDead {
			liveByTeam[player.Team]++
		}
	}

	eliminated := make(map[simulation.Team]bool, len(mode.Teams))
	for _, team := range mode.Teams {
		eliminated[team.Name] = liveByTeam[team.Name] == 0
	}
	return eliminated
}

func livePlayerCount(players []simulation.PlayerData) int {
	alive := 0
	for _, player := range players {
		if !player.IsDead {
			alive++
		}
	}
	return alive
}

func playerSurvivalGameEndResults(players []simulation.PlayerData) map[string]gameEndResult {
	if len(players) == 0 {
		return nil
	}

	deadCount := 0
	for _, player := range players {
		if player.IsDead {
			deadCount++
		}
	}
	if deadCount == 0 {
		return nil
	}

	results := make(map[string]gameEndResult, len(players))
	if deadCount == len(players) {
		for _, player := range players {
			results[string(player.ID)] = gameEndResultDraw
		}
		return results
	}

	for _, player := range players {
		if player.IsDead {
			results[string(player.ID)] = gameEndResultLose
		} else {
			results[string(player.ID)] = gameEndResultWin
		}
	}
	return results
}
