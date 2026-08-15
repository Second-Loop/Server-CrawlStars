package rooms

import (
	"fmt"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestBotControllerDetectionRangeIncludesBoundaryOnly(t *testing.T) {
	gameMap := botControllerOpenMap(31, 5)
	config := botControllerConfig(gameMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(15, 2))

	for _, test := range []struct {
		name       string
		distance   float64
		wantTarget bool
	}{
		{name: "just inside", distance: 15 - 1e-9, wantTarget: true},
		{name: "exact boundary", distance: 15, wantTarget: true},
		{name: "just outside", distance: 15 + 1e-9, wantTarget: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			enemy := botControllerPlayer(config, "enemy", simulation.TeamBlue, simulation.Vector2{
				X: bot.Pos.X + test.distance,
				Y: bot.Pos.Y,
			})
			state := botControllerState{}
			input, ok := botInputForObservation(bot, botObservation{
				roomID:      "range-room",
				gameMap:     gameMap,
				gameConfig:  config,
				players:     []simulation.PlayerData{bot, enemy},
				currentTick: 1,
			}, &state)
			if !ok {
				t.Fatal("live bot must produce an input")
			}
			if got := input.AttackDir != (simulation.Vector2{}); got != test.wantTarget {
				t.Fatalf("target detected=%t, want %t; input=%+v", got, test.wantTarget, input)
			}
			if input.PressedAttack {
				t.Fatalf("PressedAttack=true for detection-only distance; input=%+v", input)
			}
		})
	}
}

func TestBotControllerNormalAttackRangeIncludesExactBoundaryOnly(t *testing.T) {
	gameMap := botControllerOpenMap(41, 5)
	config := botControllerConfig(gameMap)
	playerType, ok := config.PlayerType(simulation.CharacterTypeShelly)
	if !ok {
		t.Fatal("missing Shelly config")
	}
	attackRange := playerType.NormalAttack.RangeTiles * config.Tile.Size
	config.Bot.DetectionRangeWorld = attackRange + 1
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(20, 2))

	for _, test := range []struct {
		name       string
		distance   float64
		wantAttack bool
	}{
		{name: "exact boundary", distance: attackRange, wantAttack: true},
		{name: "just outside", distance: attackRange + 1e-9, wantAttack: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			enemy := botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(bot.Pos, test.distance, 0))
			input, ok := botInputForObservation(bot, botObservation{
				gameMap:     gameMap,
				gameConfig:  config,
				players:     []simulation.PlayerData{bot, enemy},
				currentTick: 1,
			}, &botControllerState{})
			if !ok {
				t.Fatal("live bot must produce an input")
			}
			if input.AttackDir == (simulation.Vector2{}) {
				t.Fatalf("target was not detected at distance %v: input=%+v", test.distance, input)
			}
			if input.PressedAttack != test.wantAttack {
				t.Fatalf("PressedAttack=%t at distance %v, want %t; input=%+v", input.PressedAttack, test.distance, test.wantAttack, input)
			}
		})
	}
}

func TestBotControllerTargetTieUsesPlayerIDRegardlessOfPermutation(t *testing.T) {
	gameMap := botControllerOpenMap(9, 9)
	config := botControllerConfig(gameMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(4, 4))
	first := botControllerPlayer(config, "enemy-a", simulation.TeamBlue, simulation.Vector2{X: bot.Pos.X - 3, Y: bot.Pos.Y})
	second := botControllerPlayer(config, "enemy-b", simulation.TeamBlue, simulation.Vector2{X: bot.Pos.X + 3, Y: bot.Pos.Y})

	var want simulation.InputCommand
	for index, players := range [][]simulation.PlayerData{
		{bot, first, second},
		{second, bot, first},
		{first, second, bot},
	} {
		state := botControllerState{}
		got, ok := botInputForObservation(bot, botObservation{
			roomID:      "tie-room",
			gameMap:     gameMap,
			gameConfig:  config,
			players:     players,
			currentTick: 1,
		}, &state)
		if !ok {
			t.Fatalf("permutation %d: bot input missing", index)
		}
		if index == 0 {
			want = got
		} else if got != want {
			t.Fatalf("permutation %d input=%+v, want %+v", index, got, want)
		}
	}
	wantDirection := simulation.Vector2{X: -1}
	if want.AttackDir != wantDirection || want.MoveDir != (simulation.Vector2{X: -1, Y: 0}) {
		t.Fatalf("tie selected input=%+v, want target direction %+v and path direction -X", want, wantDirection)
	}
}

func TestBotControllerMovementPriorityIsDodgeExploreRetreatThenChase(t *testing.T) {
	gameMap := botControllerOpenMap(15, 15)
	config := botControllerConfig(gameMap)
	center := gameMap.WorldPos(7, 7)

	tests := []struct {
		name       string
		bot        simulation.PlayerData
		players    []simulation.PlayerData
		projectile *simulation.ProjectileData
		wantMove   simulation.Vector2
	}{
		{
			name: "dodge wins over low hp retreat and chase",
			bot: func() simulation.PlayerData {
				player := botControllerPlayer(config, "bot", simulation.TeamRed, center)
				player.HP = player.HP * 0.1
				return player
			}(),
			players: []simulation.PlayerData{
				botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(center, 2, 0)),
			},
			projectile: &simulation.ProjectileData{ID: "threat", OwnerID: "enemy", Pos: botControllerOffset(center, -4, -0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1},
			wantMove:   simulation.Vector2{Y: 1},
		},
		{
			name:     "explore wins without detected enemy",
			bot:      botControllerPlayer(config, "bot", simulation.TeamRed, center),
			players:  nil,
			wantMove: simulation.Vector2{},
		},
		{
			name: "retreat wins at hp boundary",
			bot: func() simulation.PlayerData {
				player := botControllerPlayer(config, "bot", simulation.TeamRed, center)
				player.HP = player.HP * 0.2
				return player
			}(),
			players: []simulation.PlayerData{
				botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(center, 2, 0)),
			},
			wantMove: simulation.Vector2{X: -1},
		},
		{
			name: "chase is final priority",
			bot:  botControllerPlayer(config, "bot", simulation.TeamRed, center),
			players: []simulation.PlayerData{
				botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(center, 2, 0)),
			},
			wantMove: simulation.Vector2{X: 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := botControllerState{}
			projectiles := []simulation.ProjectileData(nil)
			if test.projectile != nil {
				projectiles = []simulation.ProjectileData{*test.projectile}
			}
			players := append([]simulation.PlayerData{test.bot}, test.players...)
			input, ok := botInputForObservation(test.bot, botObservation{
				roomID:      "priority-room",
				gameMap:     gameMap,
				gameConfig:  config,
				players:     players,
				projectiles: projectiles,
				currentTick: 1,
			}, &state)
			if !ok {
				t.Fatal("live bot must produce an input")
			}
			if test.name == "explore wins without detected enemy" {
				if input.MoveDir == (simulation.Vector2{}) || !state.hasExploreDestination {
					t.Fatalf("explore input=%+v state=%+v, want a selected path", input, state)
				}
				return
			}
			if input.MoveDir != test.wantMove {
				t.Fatalf("MoveDir=%+v, want %+v; input=%+v state=%+v", input.MoveDir, test.wantMove, input, state)
			}
		})
	}
}

func TestBotControllerAttackIsIndependentDuringDodgeAndRetreat(t *testing.T) {
	gameMap := botControllerOpenMap(15, 15)
	config := botControllerConfig(gameMap)
	center := gameMap.WorldPos(7, 7)
	for _, test := range []struct {
		name       string
		lowHP      bool
		projectile bool
		wantMove   simulation.Vector2
	}{
		{name: "dodge", projectile: true, wantMove: simulation.Vector2{Y: 1}},
		{name: "retreat", lowHP: true, wantMove: simulation.Vector2{X: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bot := botControllerPlayer(config, "bot", simulation.TeamRed, center)
			if test.lowHP {
				bot.HP *= 0.2
			}
			enemy := botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(center, 2, 0))
			obs := botObservation{
				roomID:      "attack-independent-room",
				gameMap:     gameMap,
				gameConfig:  config,
				players:     []simulation.PlayerData{bot, enemy},
				currentTick: 10,
			}
			if test.projectile {
				obs.projectiles = []simulation.ProjectileData{{ID: "threat", OwnerID: enemy.ID, Pos: botControllerOffset(center, -4, -0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1}}
			}
			input, ok := botInputForObservation(bot, obs, &botControllerState{})
			if !ok || input.MoveDir != test.wantMove || !input.PressedAttack {
				t.Fatalf("input=%+v ok=%t, want move %+v and independent attack", input, ok, test.wantMove)
			}
			if input.AttackDir != (simulation.Vector2{X: 1}) {
				t.Fatalf("AttackDir=%+v, want target direction +X", input.AttackDir)
			}
		})
	}
}

func TestBotControllerAttackReadyAtExactTickAndIgnoresSkillReadyTick(t *testing.T) {
	gameMap := botControllerOpenMap(9, 9)
	config := botControllerConfig(gameMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(4, 4))
	bot.SkillReadyTick = 1000
	enemy := botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(bot.Pos, 2, 0))
	obs := botObservation{
		roomID:          "cadence-room",
		gameMap:         gameMap,
		gameConfig:      config,
		players:         []simulation.PlayerData{bot, enemy},
		nextAttackTicks: map[simulation.PlayerID]simulation.Tick{bot.ID: 8},
	}

	before, ok := botInputForObservation(bot, obsWithTick(obs, 7), &botControllerState{})
	if !ok || before.PressedAttack {
		t.Fatalf("before-ready input=%+v ok=%t, want no attack", before, ok)
	}
	ready, ok := botInputForObservation(bot, obsWithTick(obs, 8), &botControllerState{})
	if !ok || !ready.PressedAttack {
		t.Fatalf("exact-ready input=%+v ok=%t, want attack", ready, ok)
	}
	if ready.ClientTick != 0 || ready.PressedSkill {
		t.Fatalf("bot command transient fields=%+v, want ClientTick 0 and PressedSkill false", ready)
	}
}

func TestBotControllerExploreSeedIsCanonicalAndPermutationIndependent(t *testing.T) {
	candidates := []botTile{{x: 2, y: 0}, {x: 0, y: 1}, {x: 1, y: 1}, {x: 1, y: 0}, {x: 0, y: 0}}
	current := botTile{x: 1, y: 1}
	got, ok := selectBotExploreTile("room", "bot", candidates, current, 7)
	if !ok {
		t.Fatal("canonical candidate selection failed")
	}
	reversed := append([]botTile(nil), candidates...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	if got != (botTile{x: 0, y: 1}) {
		t.Fatalf("selected tile=%+v, want hand-derived row-major SHA candidate (0,1)", got)
	}
	permuted, ok := selectBotExploreTile("room", "bot", reversed, current, 7)
	if !ok || got != permuted {
		t.Fatalf("candidate permutation changed selected tile: original=%+v reversed=%+v", got, permuted)
	}
	otherID, ok := selectBotExploreTile("room", "bot-other", candidates, current, 7)
	if !ok {
		t.Fatal("bot ID variation selection failed")
	}
	if otherID != (botTile{x: 0, y: 0}) {
		t.Fatalf("same-epoch bot ID selected tile=%+v, want hand-derived (0,0)", otherID)
	}
	if otherID == got {
		t.Fatalf("same-epoch bot ID did not affect explore seed: bot=%+v bot-other=%+v", got, otherID)
	}
}

func TestBotControllerExploreExcludesCurrentTileAndIncrementsEpochOnlyOnSelection(t *testing.T) {
	gameMap := botControllerOpenMap(7, 7)
	config := botControllerConfig(gameMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(3, 3))
	state := botControllerState{}
	obs := botObservation{roomID: "explore-room", gameMap: gameMap, gameConfig: config, players: []simulation.PlayerData{bot}}

	first, ok := botInputForObservation(bot, obsWithTick(obs, 1), &state)
	if !ok || first.MoveDir == (simulation.Vector2{}) || !state.hasExploreDestination {
		t.Fatalf("first explore input=%+v ok=%t state=%+v", first, ok, state)
	}
	firstEpoch := state.exploreEpoch
	firstDestination := state.exploreDestination
	if firstEpoch != 1 {
		t.Fatalf("first selection epoch=%d, want 1", firstEpoch)
	}
	currentTile, ok := worldToBotTile(gameMap, bot.Pos)
	if !ok {
		t.Fatal("bot position must map to a tile")
	}
	destinationTile, ok := worldToBotTile(gameMap, firstDestination)
	if !ok || destinationTile == currentTile {
		t.Fatalf("explore destination tile=%+v, current tile=%+v; current tile must be excluded", destinationTile, currentTile)
	}

	second, ok := botInputForObservation(bot, obsWithTick(obs, 2), &state)
	if !ok || second.MoveDir == (simulation.Vector2{}) {
		t.Fatalf("cached explore input=%+v ok=%t state=%+v", second, ok, state)
	}
	if state.exploreEpoch != firstEpoch || state.exploreDestination != firstDestination {
		t.Fatalf("destination changed without arrival: epoch=%d destination=%+v, want epoch=%d destination=%+v", state.exploreEpoch, state.exploreDestination, firstEpoch, firstDestination)
	}
}

func TestBotControllerExploreArrivalAtOrBelowQuarterUnitReselectsNextTick(t *testing.T) {
	gameMap := botControllerOpenMap(7, 7)
	config := botControllerConfig(gameMap)
	goal := gameMap.WorldPos(2, 3)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, botControllerOffset(goal, 0.25, 0))
	state := botControllerState{
		exploreEpoch:          4,
		hasExploreDestination: true,
		exploreDestination:    goal,
	}
	input, ok := botInputForObservation(bot, botObservation{
		roomID:      "arrival-room",
		gameMap:     gameMap,
		gameConfig:  config,
		players:     []simulation.PlayerData{bot},
		currentTick: 5,
	}, &state)
	if !ok {
		t.Fatal("live bot must produce an input")
	}
	if state.exploreEpoch != 5 || !state.hasExploreDestination || state.exploreDestination == goal {
		t.Fatalf("arrival state=%+v, want next selected epoch 5 and a new destination", state)
	}
	if input.MoveDir == (simulation.Vector2{}) {
		t.Fatalf("arrival reselect should produce a path input when another candidate exists: %+v", input)
	}
}

func TestBotControllerExplorePathFailureDiscardsDestinationUntilNextTick(t *testing.T) {
	gameMap := simulation.MapData{
		Width: 3, Height: 1, TileSize: 1,
		Map: [][]simulation.TileType{{simulation.TileGround, simulation.TileWall, simulation.TileGround}},
	}
	config := botControllerConfig(gameMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(0, 0))
	state := botControllerState{
		exploreEpoch:          1,
		hasExploreDestination: true,
		exploreDestination:    gameMap.WorldPos(2, 0),
	}
	obs := botObservation{roomID: "failure-room", gameMap: gameMap, gameConfig: config, players: []simulation.PlayerData{bot}}
	failed, ok := botInputForObservation(bot, obsWithTick(obs, 1), &state)
	if !ok || failed.MoveDir != (simulation.Vector2{}) {
		t.Fatalf("failed path input=%+v ok=%t, want zero movement", failed, ok)
	}
	if state.hasExploreDestination || state.exploreEpoch != 1 {
		t.Fatalf("path failure state=%+v, want destination discarded without same-tick reselection", state)
	}

	_, ok = botInputForObservation(bot, obsWithTick(obs, 2), &state)
	if !ok || state.exploreEpoch != 2 {
		t.Fatalf("next tick state=%+v, want a new epoch selection", state)
	}
}

func TestBotControllerPathCacheReusesAndInvalidatesOnStartOrGoal(t *testing.T) {
	gameMap := botControllerOpenMap(7, 5)
	start := gameMap.WorldPos(1, 2)
	goal := gameMap.WorldPos(3, 2)
	state := botControllerState{}
	step := simulation.DefaultPlayerSpeed * simulation.TickDuration

	first, ok := cachedBotPathDirection(gameMap, start, goal, step, &state)
	if !ok || first != (simulation.Vector2{X: 1}) {
		t.Fatalf("first cached path=%+v ok=%t, want +X", first, ok)
	}
	if state.cachedPathNext != (botTile{x: 2, y: 2}) {
		t.Fatalf("cached next tile=%+v, want (2,2)", state.cachedPathNext)
	}
	offsetWithinStartTile := simulation.Vector2{X: start.X, Y: start.Y + 0.2}
	reused, ok := cachedBotPathDirection(gameMap, offsetWithinStartTile, goal, step, &state)
	if !ok || reused.X != 0 || reused.Y >= 0 {
		t.Fatalf("same start/goal cache steering=%+v ok=%t, want fresh perpendicular centering toward the cached tile step", reused, ok)
	}
	if state.cachedPathNext != (botTile{x: 2, y: 2}) {
		t.Fatalf("same start/goal replaced cached next tile: %+v", state.cachedPathNext)
	}
	newStart := gameMap.WorldPos(1, 1)
	invalidatedStart, ok := cachedBotPathDirection(gameMap, newStart, goal, step, &state)
	if !ok || state.cachedPathStart != (botTile{x: 1, y: 1}) {
		t.Fatalf("start change did not replace cache: direction=%+v state=%+v ok=%t", invalidatedStart, state, ok)
	}
	newGoal := gameMap.WorldPos(3, 1)
	invalidatedGoal, ok := cachedBotPathDirection(gameMap, newStart, newGoal, step, &state)
	if !ok || state.cachedPathGoal != (botTile{x: 3, y: 1}) {
		t.Fatalf("goal change did not replace cache: direction=%+v state=%+v ok=%t", invalidatedGoal, state, ok)
	}
}

func TestBotControllerAvoidsHeadOnPlayerMovementDeadlock(t *testing.T) {
	for _, distance := range []float64{1.0666666666666667, 1.10} {
		t.Run(fmt.Sprintf("distance-%.3f", distance), func(t *testing.T) {
			gameMap := botControllerOpenMap(7, 7)
			gameMap.MaxPlayers = 6
			config := botControllerConfig(gameMap)
			center := gameMap.WorldPos(3, 3)
			botA := botControllerPlayer(config, "bot-a", simulation.TeamRed, botControllerOffset(center, 0, distance/2))
			botB := botControllerPlayer(config, "bot-b", simulation.TeamBlue, botControllerOffset(center, 0, -distance/2))
			botA.IsBot = true
			botB.IsBot = true
			observation := botObservation{
				roomID:      "head-on",
				gameMap:     gameMap,
				gameConfig:  config,
				players:     []simulation.PlayerData{botA, botB},
				currentTick: 1,
				nextAttackTicks: map[simulation.PlayerID]simulation.Tick{
					botA.ID: 2,
					botB.ID: 2,
				},
			}
			botIDs := map[simulation.PlayerID]struct{}{botA.ID: {}, botB.ID: {}}
			inputs := mergedTickInputsAtTick(
				nil,
				observation,
				map[simulation.PlayerID]*botControllerState{},
				botIDs,
				botIDs,
			)
			if len(inputs) != 2 {
				t.Fatalf("head-on bot inputs=%+v, want two", inputs)
			}
			state := simulation.NewStateWithConfig(
				[]simulation.PlayerData{botA, botB},
				simulation.Config{Map: gameMap, Game: config},
			)
			snapshot := state.Step(inputs)
			if snapshot.Players[0].Pos == botA.Pos || snapshot.Players[1].Pos == botB.Pos {
				t.Fatalf(
					"head-on bot positions remained blocked: inputs=%+v players=%+v",
					inputs,
					snapshot.Players,
				)
			}
		})
	}
}

func TestBotControllerDodgeExcludesOwnAllyDestroyedBehindAndFarProjectiles(t *testing.T) {
	gameMap := botControllerOpenMap(9, 9)
	config := botControllerConfig(gameMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(4, 4))
	ally := botControllerPlayer(config, "ally", simulation.TeamRed, botControllerOffset(bot.Pos, 0, 2))
	enemy := botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(bot.Pos, 2, 0))

	for _, test := range []struct {
		name       string
		projectile simulation.ProjectileData
		players    []simulation.PlayerData
	}{
		{name: "own", projectile: simulation.ProjectileData{ID: "own", OwnerID: bot.ID, Pos: botControllerOffset(bot.Pos, -4, 0), Dir: simulation.Vector2{X: 1}, Radius: 0.1}, players: []simulation.PlayerData{bot, enemy}},
		{name: "ally", projectile: simulation.ProjectileData{ID: "ally", OwnerID: ally.ID, Pos: botControllerOffset(bot.Pos, -4, -0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1}, players: []simulation.PlayerData{bot, ally, enemy}},
		{name: "destroyed", projectile: simulation.ProjectileData{ID: "destroyed", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, -4, -0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1, IsDestroyed: true}, players: []simulation.PlayerData{bot, enemy}},
		{name: "behind", projectile: simulation.ProjectileData{ID: "behind", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, 4, -0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1}, players: []simulation.PlayerData{bot, enemy}},
		{name: "outside look ahead", projectile: simulation.ProjectileData{ID: "far", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, -9, 0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1}, players: []simulation.PlayerData{bot, enemy}},
	} {
		t.Run(test.name, func(t *testing.T) {
			obs := botObservation{gameMap: gameMap, gameConfig: config, players: test.players, projectiles: []simulation.ProjectileData{test.projectile}}
			if got, ok := botDodgeDirection(bot, obs); ok || got != (simulation.Vector2{}) {
				t.Fatalf("dodge direction=%+v ok=%t, want no threat for %s", got, ok, test.name)
			}
		})
	}
}

func TestBotControllerDodgeSumsAwayVectorsInProjectileIDOrder(t *testing.T) {
	gameMap := botControllerOpenMap(9, 9)
	config := botControllerConfig(gameMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(4, 4))
	enemy := botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(bot.Pos, 2, 0))
	projectiles := []simulation.ProjectileData{
		{ID: "b", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, -4, -0.75), Dir: simulation.Vector2{X: 1}, Radius: 0.1},
		{ID: "a", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, -4, -0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1},
	}
	obs := botObservation{gameMap: gameMap, gameConfig: config, players: []simulation.PlayerData{bot, enemy}, projectiles: projectiles}
	got, ok := botDodgeDirection(bot, obs)
	if !ok || got != (simulation.Vector2{Y: 1}) {
		t.Fatalf("summed dodge=%+v ok=%t, want normalized +Y", got, ok)
	}
	obs.projectiles[0], obs.projectiles[1] = obs.projectiles[1], obs.projectiles[0]
	permuted, ok := botDodgeDirection(bot, obs)
	if !ok || permuted != got {
		t.Fatalf("projectile permutation changed dodge: got=%+v permuted=%+v ok=%t", got, permuted, ok)
	}
}

func TestBotControllerDodgeCancellationUsesEarliestThenProjectileID(t *testing.T) {
	gameMap := botControllerOpenMap(11, 11)
	config := botControllerConfig(gameMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(5, 5))
	enemy := botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(bot.Pos, 2, 0))

	for _, test := range []struct {
		name        string
		projectiles []simulation.ProjectileData
		want        simulation.Vector2
	}{
		{
			name: "earliest collision",
			projectiles: []simulation.ProjectileData{
				{ID: "late", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, -4, -0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1},
				{ID: "early", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, -3, 0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1},
			},
			want: simulation.Vector2{Y: 1},
		},
		{
			name: "projectile ID tie",
			projectiles: []simulation.ProjectileData{
				{ID: "b", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, 4, 0.5), Dir: simulation.Vector2{X: -1}, Radius: 0.1},
				{ID: "a", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, -4, -0.5), Dir: simulation.Vector2{X: 1}, Radius: 0.1},
			},
			want: simulation.Vector2{Y: 1},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := botDodgeDirection(bot, botObservation{gameMap: gameMap, gameConfig: config, players: []simulation.PlayerData{bot, enemy}, projectiles: test.projectiles})
			if !ok || got != test.want {
				t.Fatalf("fallback dodge=%+v ok=%t, want %+v", got, ok, test.want)
			}
		})
	}
}

func TestBotControllerDodgeCancellationUsesForwardDistanceBeforeCollisionBoundary(t *testing.T) {
	gameMap := botControllerOpenMap(11, 11)
	config := botControllerConfig(gameMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, gameMap.WorldPos(5, 5))
	enemy := botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(bot.Pos, 2, 0))
	projectiles := []simulation.ProjectileData{
		// This threat is closer along its ray (d=3), but its larger lateral
		// offset makes its collision boundary farther than the second threat.
		{ID: "forward-first", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, -3, -0.9), Dir: simulation.Vector2{X: 1}, Radius: 0.1},
		// Its centerline distance is farther (d=3.2), while its collision
		// boundary is earlier. The binding requires choosing forward-first.
		{ID: "collision-first", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, 3.2, 0.5), Dir: simulation.Vector2{X: -1}, Radius: 0.1},
	}

	got, ok := botDodgeDirection(bot, botObservation{
		gameMap:     gameMap,
		gameConfig:  config,
		players:     []simulation.PlayerData{bot, enemy},
		projectiles: projectiles,
	})
	if !ok || got != (simulation.Vector2{Y: 1}) {
		t.Fatalf("forward-distance fallback=%+v ok=%t, want +90 of forward-first threat (+Y)", got, ok)
	}
}

func TestBotControllerDodgeFallbackChecksPlusAndMinusNinetyAndBothBlocked(t *testing.T) {
	baseMap := simulation.MapData{
		Width: 3, Height: 3, TileSize: 0.3,
		Map: [][]simulation.TileType{
			{simulation.TileGround, simulation.TileGround, simulation.TileGround},
			{simulation.TileGround, simulation.TileGround, simulation.TileGround},
			{simulation.TileGround, simulation.TileGround, simulation.TileGround},
		},
	}
	config := botControllerConfig(baseMap)
	bot := botControllerPlayer(config, "bot", simulation.TeamRed, baseMap.WorldPos(1, 1))
	bot.Radius = 0.1
	bot.Speed = 2
	enemy := botControllerPlayer(config, "enemy", simulation.TeamBlue, botControllerOffset(bot.Pos, 1, 0))
	threat := simulation.ProjectileData{ID: "threat", OwnerID: enemy.ID, Pos: botControllerOffset(bot.Pos, -1, 0), Dir: simulation.Vector2{X: 1}, Radius: 0.1}

	plusBlocked := cloneMapData(baseMap)
	plusBlocked.Map[0][1] = simulation.TileWall
	got, ok := botDodgeDirection(bot, botObservation{gameMap: plusBlocked, gameConfig: config, players: []simulation.PlayerData{bot, enemy}, projectiles: []simulation.ProjectileData{threat}})
	if !ok || got != (simulation.Vector2{Y: -1}) {
		t.Fatalf("+90 blocked fallback=%+v ok=%t, want -90", got, ok)
	}

	bothBlocked := cloneMapData(plusBlocked)
	bothBlocked.Map[2][1] = simulation.TileWall
	got, ok = botDodgeDirection(bot, botObservation{gameMap: bothBlocked, gameConfig: config, players: []simulation.PlayerData{bot, enemy}, projectiles: []simulation.ProjectileData{threat}})
	if !ok || got != (simulation.Vector2{}) {
		t.Fatalf("both-blocked fallback=%+v ok=%t, want zero movement with threat retained", got, ok)
	}
}

func botControllerConfig(gameMap simulation.MapData) simulation.GameConfig {
	config, err := simulation.StaticGameConfig().SelectMode(simulation.GameModeDuel1v1)
	if err != nil {
		panic(err)
	}
	config.Map = gameMap
	config.Tile.Size = gameMap.TileSize
	if config.Tile.Size <= 0 {
		config.Tile.Size = simulation.TileSize
	}
	return config
}

func botControllerOpenMap(width, height int) simulation.MapData {
	gameMap := simulation.MapData{Width: width, Height: height, TileSize: simulation.TileSize, Map: make([][]simulation.TileType, height)}
	for y := range gameMap.Map {
		gameMap.Map[y] = make([]simulation.TileType, width)
		for x := range gameMap.Map[y] {
			gameMap.Map[y][x] = simulation.TileGround
		}
	}
	return gameMap
}

func botControllerPlayer(config simulation.GameConfig, id simulation.PlayerID, team simulation.Team, pos simulation.Vector2) simulation.PlayerData {
	playerType, ok := config.PlayerType(simulation.CharacterTypeShelly)
	if !ok {
		panic("missing Shelly test config")
	}
	return simulation.PlayerData{
		ID: id, Team: team, IsBot: id == "bot", CharacterType: playerType.CharacterType,
		Pos: pos, Speed: playerType.Speed, Radius: playerType.Radius, HP: playerType.HP,
	}
}

func botControllerOffset(pos simulation.Vector2, x, y float64) simulation.Vector2 {
	return simulation.Vector2{X: pos.X + x, Y: pos.Y + y}
}

func obsWithTick(observation botObservation, tick simulation.Tick) botObservation {
	observation.currentTick = tick
	return observation
}

func cloneMapData(gameMap simulation.MapData) simulation.MapData {
	cloned := gameMap
	cloned.Map = make([][]simulation.TileType, len(gameMap.Map))
	for y := range gameMap.Map {
		cloned.Map[y] = append([]simulation.TileType(nil), gameMap.Map[y]...)
	}
	return cloned
}
