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
	liveBotIDs map[simulation.PlayerID]struct{},
	authoritativeBotIDs map[simulation.PlayerID]struct{},
) []simulation.InputCommand {
	botIDs := make(map[simulation.PlayerID]struct{})
	for playerID := range authoritativeBotIDs {
		botIDs[playerID] = struct{}{}
	}
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
		if _, isLive := liveBotIDs[player.ID]; !isLive {
			continue
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

// cloneBotAttackTicks returns an immutable observation view of room-owned
// cadence state. The caller owns the returned map and may not publish changes
// back into the room's authoritative cadence map.
func cloneBotAttackTicks(source map[simulation.PlayerID]simulation.Tick) map[simulation.PlayerID]simulation.Tick {
	if source == nil {
		return nil
	}
	cloned := make(map[simulation.PlayerID]simulation.Tick, len(source))
	for playerID, tick := range source {
		cloned[playerID] = tick
	}
	return cloned
}

func botParticipantIDs(participants []playerResponse) map[simulation.PlayerID]struct{} {
	botIDs := make(map[simulation.PlayerID]struct{})
	for _, participant := range participants {
		if participant.IsBot {
			botIDs[simulation.PlayerID(participant.ID)] = struct{}{}
		}
	}
	return botIDs
}

// pruneBotControllerStateLocked keeps room-owned controller and cadence state
// aligned with authoritative live bot participants in the previous snapshot.
// The caller holds room.mu. The returned IDs are the only bots allowed to
// generate commands or receive snapshot-approved cadence updates for this
// tick.
func (r *room) pruneBotControllerStateLocked() map[simulation.PlayerID]struct{} {
	if r.botControllerStates == nil {
		r.botControllerStates = make(map[simulation.PlayerID]*botControllerState)
	}
	participantBots := botParticipantIDs(r.Players)
	activeBots := make(map[simulation.PlayerID]struct{}, len(participantBots))
	for _, player := range r.lastPlayers {
		if !player.IsBot {
			continue
		}
		if _, ok := participantBots[player.ID]; !ok {
			continue
		}
		if player.IsDead || player.HP <= 0 {
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
	return activeBots
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
