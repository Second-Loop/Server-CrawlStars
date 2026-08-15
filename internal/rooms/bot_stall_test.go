package rooms

import (
	"fmt"
	"math"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	serverconfig "github.com/Second-Loop/Server-CrawlStars/server-config"
)

const productionBotProbeTicks = 900

type productionBotProgressFixture struct {
	mode             string
	config           simulation.GameConfig
	state            *simulation.State
	players          []simulation.PlayerData
	projectiles      []simulation.ProjectileData
	controllerStates map[simulation.PlayerID]*botControllerState
	botIDs           map[simulation.PlayerID]struct{}
	nextAttackTicks  map[simulation.PlayerID]simulation.Tick
	tick             simulation.Tick
}

type botProgressProbe struct {
	Tick       simulation.Tick
	BotID      simulation.PlayerID
	Input      simulation.InputCommand
	Before     simulation.Vector2
	After      simulation.Vector2
	HP         float64
	IsDead     bool
	AllPlayers []simulation.PlayerData
	AllInputs  []simulation.InputCommand
}

func newProductionBotProgressFixture(t *testing.T, mode string) *productionBotProgressFixture {
	t.Helper()
	config, err := simulation.LoadGameConfig(serverconfig.Reader())
	if err != nil {
		t.Fatalf("load production config: %v", err)
	}
	config, err = config.SelectMode(mode)
	if err != nil {
		t.Fatalf("select mode %q: %v", mode, err)
	}

	ids := []simulation.PlayerID{"human", "bot-a", "bot-b", "bot-c", "bot-d", "bot-e"}
	assignments := simulation.PlayerAssignments(ids, config)
	if len(assignments) != len(ids) {
		t.Fatalf("assignments=%d, want %d", len(assignments), len(ids))
	}
	players := make([]simulation.PlayerData, len(assignments))
	botIDs := make(map[simulation.PlayerID]struct{}, len(assignments)-1)
	controllerStates := make(map[simulation.PlayerID]*botControllerState, len(assignments)-1)
	nextAttackTicks := make(map[simulation.PlayerID]simulation.Tick, len(assignments)-1)
	for index, assignment := range assignments {
		characterType := simulation.CharacterType(index % 3)
		playerType, ok := config.PlayerType(characterType)
		if !ok {
			t.Fatalf("missing player type %d", characterType)
		}
		isBot := index > 0
		players[index] = simulation.PlayerData{
			ID:            assignment.ID,
			Team:          assignment.Team,
			Slot:          assignment.Slot,
			IsBot:         isBot,
			CharacterType: characterType,
			Pos:           assignment.SpawnPosition,
			Speed:         playerType.Speed,
			Radius:        playerType.Radius,
			HP:            playerType.HP,
		}
		if isBot {
			botIDs[assignment.ID] = struct{}{}
			controllerStates[assignment.ID] = &botControllerState{}
			// This probe isolates movement progress. Combat-specific controller
			// behavior has separate focused tests, so no participant dies while
			// persistent movement cancellation is being measured.
			nextAttackTicks[assignment.ID] = productionBotProbeTicks + 1
		}
	}

	return &productionBotProgressFixture{
		mode:             mode,
		config:           config,
		state:            simulation.NewStateWithConfig(players, simulation.Config{Game: config}),
		players:          append([]simulation.PlayerData(nil), players...),
		controllerStates: controllerStates,
		botIDs:           botIDs,
		nextAttackTicks:  nextAttackTicks,
	}
}

func stepProductionBots(t *testing.T, fixture *productionBotProgressFixture) []botProgressProbe {
	t.Helper()
	beforeByID := make(map[simulation.PlayerID]simulation.PlayerData, len(fixture.players))
	for _, player := range fixture.players {
		beforeByID[player.ID] = player
	}
	observation := botObservation{
		roomID:          "sl-121-" + fixture.mode,
		gameMap:         fixture.config.Map,
		gameConfig:      fixture.config,
		players:         append([]simulation.PlayerData(nil), fixture.players...),
		projectiles:     append([]simulation.ProjectileData(nil), fixture.projectiles...),
		currentTick:     fixture.tick + 1,
		nextAttackTicks: cloneBotAttackTicks(fixture.nextAttackTicks),
	}
	inputs := mergedTickInputsAtTick(
		nil,
		observation,
		fixture.controllerStates,
		fixture.botIDs,
		fixture.botIDs,
	)
	inputByID := make(map[simulation.PlayerID]simulation.InputCommand, len(inputs))
	for _, input := range inputs {
		if _, exists := inputByID[input.PlayerID]; exists {
			t.Fatalf("tick %d duplicate input for %q: %+v", observation.currentTick, input.PlayerID, inputs)
		}
		inputByID[input.PlayerID] = input
	}

	snapshot := fixture.state.Step(inputs)
	fixture.tick = snapshot.Tick
	fixture.players = append([]simulation.PlayerData(nil), snapshot.Players...)
	fixture.projectiles = append([]simulation.ProjectileData(nil), snapshot.Projectiles...)

	probes := make([]botProgressProbe, 0, len(fixture.botIDs))
	for _, player := range snapshot.Players {
		if _, ok := fixture.botIDs[player.ID]; !ok {
			continue
		}
		before := beforeByID[player.ID]
		probes = append(probes, botProgressProbe{
			Tick:       snapshot.Tick,
			BotID:      player.ID,
			Input:      inputByID[player.ID],
			Before:     before.Pos,
			After:      player.Pos,
			HP:         player.HP,
			IsDead:     player.IsDead,
			AllPlayers: append([]simulation.PlayerData(nil), snapshot.Players...),
			AllInputs:  append([]simulation.InputCommand(nil), inputs...),
		})
	}
	return probes
}

func TestProductionBotProgressProbeUsesRealControllerAndStateStep(t *testing.T) {
	fixture := newProductionBotProgressFixture(t, simulation.GameModeSolo)
	probes := stepProductionBots(t, fixture)
	if fixture.tick != 1 {
		t.Fatalf("snapshot tick=%d, want 1", fixture.tick)
	}
	if len(probes) != 5 {
		t.Fatalf("bot probes=%d, want 5: %+v", len(probes), probes)
	}
	seen := make(map[simulation.PlayerID]struct{}, len(probes))
	for _, probe := range probes {
		if _, duplicate := seen[probe.BotID]; duplicate {
			t.Fatalf("duplicate bot probe for %q", probe.BotID)
		}
		seen[probe.BotID] = struct{}{}
		if probe.Input.PlayerID != probe.BotID {
			t.Fatalf("bot %q input=%+v, want authoritative ID", probe.BotID, probe.Input)
		}
		if probe.Input.ClientTick != 0 || probe.Input.PressedSkill {
			t.Fatalf("bot %q transient input=%+v, want ClientTick 0 and PressedSkill false", probe.BotID, probe.Input)
		}
	}
}

func TestProductionMapBotsDoNotRemainMovementBlocked(t *testing.T) {
	for _, mode := range []string{simulation.GameModeSolo, simulation.GameModeTeam} {
		t.Run(mode, func(t *testing.T) {
			fixture := newProductionBotProgressFixture(t, mode)
			blockedTicks := make(map[simulation.PlayerID]int, len(fixture.botIDs))
			for range productionBotProbeTicks {
				for _, probe := range stepProductionBots(t, fixture) {
					positionChanged := probe.Before != probe.After
					if !probe.IsDead && !positionChanged {
						blockedTicks[probe.BotID]++
					} else {
						blockedTicks[probe.BotID] = 0
					}
					if blockedTicks[probe.BotID] >= 90 {
						t.Fatalf(
							"live bot %q remained fixed for %d ticks through tick %d; %s",
							probe.BotID,
							blockedTicks[probe.BotID],
							probe.Tick,
							formatBotStallDiagnostic(fixture, probe),
						)
					}
				}
			}
		})
	}
}

func formatBotStallDiagnostic(fixture *productionBotProgressFixture, probe botProgressProbe) string {
	gameMap := fixture.config.Map
	tile, _ := worldToBotTile(gameMap, probe.Before)
	state := fixture.controllerStates[probe.BotID]
	tickRate := fixture.config.TickRate
	if tickRate <= 0 {
		tickRate = simulation.TickRate
	}
	step := simulation.Vector2{
		X: probe.Input.MoveDir.X * 2 / float64(tickRate),
		Y: probe.Input.MoveDir.Y * 2 / float64(tickRate),
	}
	geometry, _ := botMapGeometryFor(gameMap)
	nextX := simulation.Vector2{X: probe.Before.X + step.X, Y: probe.Before.Y}
	nextY := simulation.Vector2{X: probe.Before.X, Y: probe.Before.Y + step.Y}
	nearby := make([]string, 0, len(probe.AllPlayers)-1)
	inputsByID := make(map[simulation.PlayerID]simulation.InputCommand, len(probe.AllInputs))
	for _, input := range probe.AllInputs {
		inputsByID[input.PlayerID] = input
	}
	for _, player := range probe.AllPlayers {
		if player.ID == probe.BotID || player.IsDead {
			continue
		}
		distance := math.Hypot(player.Pos.X-probe.Before.X, player.Pos.Y-probe.Before.Y)
		if distance <= 2 {
			nearby = append(nearby, fmt.Sprintf(
				"%s@%+v d=%.3f move=%+v",
				player.ID,
				player.Pos,
				distance,
				inputsByID[player.ID].MoveDir,
			))
		}
	}
	return fmt.Sprintf(
		"pos=%+v move=%+v tile=%+v neighborhood=%s mapCollision(nextX=%t,nextY=%t) state=%+v nearby=%v",
		probe.Before,
		probe.Input.MoveDir,
		tile,
		formatBotMapNeighborhood(gameMap, tile, 2),
		botMapCollidesWithPlayer(geometry, gameMap, nextX, 0.5),
		botMapCollidesWithPlayer(geometry, gameMap, nextY, 0.5),
		state,
		nearby,
	)
}

func formatBotMapNeighborhood(gameMap simulation.MapData, center botTile, radius int) string {
	rows := make([]string, 0, radius*2+1)
	for y := center.y - radius; y <= center.y+radius; y++ {
		values := make([]int, 0, radius*2+1)
		for x := center.x - radius; x <= center.x+radius; x++ {
			if y < 0 || y >= gameMap.Height || x < 0 || x >= gameMap.Width {
				values = append(values, -1)
				continue
			}
			values = append(values, int(gameMap.Map[y][x]))
		}
		rows = append(rows, fmt.Sprintf("y%d=%v", y, values))
	}
	return fmt.Sprintf("%v", rows)
}
