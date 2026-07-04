package rooms

import "github.com/Second-Loop/Server-CrawlStars/internal/simulation"

type gameEndResult string

const (
	gameEndResultWin  gameEndResult = "Win"
	gameEndResultLose gameEndResult = "Lose"
	gameEndResultDraw gameEndResult = "Draw"
)

func (r gameEndResult) String() string {
	return string(r)
}

func calculateGameEndResults(gameConfig simulation.GameConfig, snapshot simulation.Snapshot) map[string]gameEndResult {
	switch gameConfig.Mode.ID {
	case simulation.GameModeDuel1v1:
		return duelGameEndResults(snapshot.Players)
	default:
		// Non-duel mode rules are not active yet. Keep the current player-survival
		// result rule for debug/custom rooms until a mode-specific issue defines it.
		return playerSurvivalGameEndResults(snapshot.Players)
	}
}

func duelGameEndResults(players []simulation.PlayerData) map[string]gameEndResult {
	return playerSurvivalGameEndResults(players)
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
