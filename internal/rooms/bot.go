package rooms

import (
	"math"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func nearestLiveEnemy(
	bot simulation.PlayerData,
	players []simulation.PlayerData,
) (simulation.PlayerData, bool) {
	var selected simulation.PlayerData
	var selectedDistance float64
	found := false
	for _, candidate := range players {
		if candidate.ID == bot.ID || candidate.Team == bot.Team || candidate.IsDead {
			continue
		}
		dx := candidate.Pos.X - bot.Pos.X
		dy := candidate.Pos.Y - bot.Pos.Y
		distance := dx*dx + dy*dy
		if !found || distance < selectedDistance ||
			(distance == selectedDistance && candidate.ID < selected.ID) {
			selected = candidate
			selectedDistance = distance
			found = true
		}
	}
	return selected, found
}

func botInputFor(
	bot simulation.PlayerData,
	players []simulation.PlayerData,
) (simulation.InputCommand, bool) {
	if !bot.IsBot || bot.IsDead {
		return simulation.InputCommand{}, false
	}
	target, ok := nearestLiveEnemy(bot, players)
	if !ok {
		return simulation.InputCommand{}, false
	}
	direction := unitDirection(bot.Pos, target.Pos)
	return simulation.InputCommand{
		PlayerID:      bot.ID,
		MoveDir:       direction,
		AttackDir:     direction,
		PressedAttack: true,
	}, true
}

func unitDirection(from simulation.Vector2, to simulation.Vector2) simulation.Vector2 {
	dx := to.X - from.X
	dy := to.Y - from.Y
	length := math.Hypot(dx, dy)
	if length == 0 {
		return simulation.Vector2{X: 1, Y: 0}
	}
	return simulation.Vector2{X: dx / length, Y: dy / length}
}
