package rooms

import (
	"math"
	"sort"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func mergedTickInputs(
	pending map[string]simulation.InputCommand,
	players []simulation.PlayerData,
) []simulation.InputCommand {
	botIDs := make(map[simulation.PlayerID]struct{})
	for _, player := range players {
		if player.IsBot {
			botIDs[player.ID] = struct{}{}
		}
	}
	byPlayer := make(map[simulation.PlayerID]simulation.InputCommand, len(pending)+len(botIDs))
	for playerID, input := range pending {
		authoritativeID := simulation.PlayerID(playerID)
		if _, isBot := botIDs[authoritativeID]; isBot {
			continue
		}
		input.PlayerID = authoritativeID
		byPlayer[authoritativeID] = input
	}
	for _, player := range players {
		if !player.IsBot {
			continue
		}
		delete(byPlayer, player.ID)
		if input, ok := botInputForAtTick(player, players, 1, nil); ok {
			byPlayer[player.ID] = input
		}
	}
	return sortedTickInputs(byPlayer)
}

func mergedTickInputsAtTick(
	pending map[string]simulation.InputCommand,
	observation botObservation,
	controllerStates map[simulation.PlayerID]*botControllerState,
) []simulation.InputCommand {
	botIDs := make(map[simulation.PlayerID]struct{})
	for _, player := range observation.players {
		if player.IsBot {
			botIDs[player.ID] = struct{}{}
		}
	}

	byPlayer := make(
		map[simulation.PlayerID]simulation.InputCommand,
		len(pending)+len(botIDs),
	)
	for playerID, input := range pending {
		authoritativeID := simulation.PlayerID(playerID)
		if _, isBot := botIDs[authoritativeID]; isBot {
			continue
		}
		input.PlayerID = authoritativeID
		byPlayer[authoritativeID] = input
	}
	for _, player := range observation.players {
		if !player.IsBot {
			continue
		}
		delete(byPlayer, player.ID)
		// A zero-HP observation without a live target is already terminal for
		// this bot. Do not turn that invalid/dead state into an explore command
		// while a reconnect expiry is being applied to the same snapshot.
		if player.HP <= 0 {
			if _, hasTarget := botTargetForObservation(player, observation); !hasTarget {
				continue
			}
		}
		state := controllerStates[player.ID]
		if state == nil {
			state = &botControllerState{}
			if controllerStates != nil {
				controllerStates[player.ID] = state
			}
		}
		if input, ok := botInputForObservation(player, observation, state); ok {
			byPlayer[player.ID] = input
		}
	}

	return sortedTickInputs(byPlayer)
}

func sortedTickInputs(byPlayer map[simulation.PlayerID]simulation.InputCommand) []simulation.InputCommand {
	inputs := make([]simulation.InputCommand, 0, len(byPlayer))
	for _, input := range byPlayer {
		inputs = append(inputs, input)
	}
	sort.Slice(inputs, func(i int, j int) bool {
		return inputs[i].PlayerID < inputs[j].PlayerID
	})
	return inputs
}

// pruneBotControllerStateLocked keeps room-owned controller and cadence state
// aligned with the intersection of bot participants and the previous
// authoritative player snapshot. The caller holds room.mu.
func (r *room) pruneBotControllerStateLocked() {
	if r.botControllerStates == nil {
		r.botControllerStates = make(map[simulation.PlayerID]*botControllerState)
	}
	participantBots := make(map[simulation.PlayerID]struct{})
	for _, participant := range r.Players {
		if participant.IsBot {
			participantBots[simulation.PlayerID(participant.ID)] = struct{}{}
		}
	}
	activeBots := make(map[simulation.PlayerID]struct{}, len(participantBots))
	for _, player := range r.lastPlayers {
		if !player.IsBot {
			continue
		}
		if _, ok := participantBots[player.ID]; !ok {
			continue
		}
		activeBots[player.ID] = struct{}{}
	}
	for playerID := range r.botControllerStates {
		if _, ok := activeBots[playerID]; !ok {
			delete(r.botControllerStates, playerID)
		}
	}
	for playerID := range r.nextBotAttackTicks {
		if _, ok := activeBots[playerID]; !ok {
			delete(r.nextBotAttackTicks, playerID)
		}
	}
}

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
	return botInputForAtTick(bot, players, 1, nil)
}

func botInputForAtTick(
	bot simulation.PlayerData,
	players []simulation.PlayerData,
	currentTick simulation.Tick,
	nextAttackTicks map[simulation.PlayerID]simulation.Tick,
) (simulation.InputCommand, bool) {
	if !bot.IsBot || bot.IsDead {
		return simulation.InputCommand{}, false
	}
	target, ok := nearestLiveEnemy(bot, players)
	if !ok {
		return simulation.InputCommand{}, false
	}
	direction := unitDirection(bot.Pos, target.Pos)
	pressedAttack := true
	if nextAttackTick, ok := nextAttackTicks[bot.ID]; ok && currentTick < nextAttackTick {
		pressedAttack = false
	}
	return simulation.InputCommand{
		PlayerID:      bot.ID,
		MoveDir:       direction,
		AttackDir:     direction,
		PressedAttack: pressedAttack,
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
