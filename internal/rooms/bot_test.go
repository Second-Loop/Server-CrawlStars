package rooms

import (
	"math"
	"reflect"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func authoritativeKeyFixture() (
	[]simulation.PlayerData,
	map[string]simulation.InputCommand,
) {
	players := []simulation.PlayerData{
		{ID: "player-a", Team: simulation.TeamRed},
		{ID: "player-b", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 3}, IsBot: true},
		{ID: "player-c", Team: simulation.TeamBlue},
	}
	pending := map[string]simulation.InputCommand{
		"player-c": {PlayerID: "spoof-z", MoveDir: simulation.Vector2{Y: -1}},
		"player-a": {PlayerID: "spoof-a", MoveDir: simulation.Vector2{Y: 1}},
		"player-b": {PlayerID: "player-a", MoveDir: simulation.Vector2{X: -99}},
	}
	return players, pending
}

func TestMergedTickInputsUsesMapKeyAsAuthoritativePlayerID(t *testing.T) {
	players, pending := authoritativeKeyFixture()
	got := mergedTickInputs(pending, players)

	if want := []simulation.PlayerID{"player-a", "player-b", "player-c"}; !reflect.DeepEqual(inputPlayerIDs(got), want) {
		t.Fatalf("merged input IDs = %v, want %v; inputs=%+v", inputPlayerIDs(got), want, got)
	}
	if human := playerInputByID(t, got, "player-a"); human.PlayerID != "player-a" ||
		human.MoveDir != (simulation.Vector2{Y: 1}) {
		t.Fatalf("map key must replace human command value ID: %+v", human)
	}
	bot := playerInputByID(t, got, "player-b")
	if bot.PlayerID != "player-b" || bot.MoveDir == (simulation.Vector2{X: -99}) || !bot.PressedAttack {
		t.Fatalf("bot pending command must be replaced by controller input: %+v", bot)
	}
}

func TestMergedTickInputsPreservesHumanClientTickAndKeepsBotTickZero(t *testing.T) {
	players := []simulation.PlayerData{
		{ID: "human", Team: simulation.TeamRed},
		{ID: "bot", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 3}, IsBot: true},
	}
	pending := map[string]simulation.InputCommand{
		"human": {PlayerID: "spoof", ClientTick: 17, MoveDir: simulation.Vector2{Y: 1}},
		"bot":   {PlayerID: "human", ClientTick: 99, MoveDir: simulation.Vector2{X: -99}},
	}

	got := mergedTickInputs(pending, players)
	human := playerInputByID(t, got, "human")
	if human.ClientTick != 17 || human.MoveDir != (simulation.Vector2{Y: 1}) {
		t.Fatalf("human command lost ClientTick or payload: %+v", human)
	}
	bot := playerInputByID(t, got, "bot")
	if bot.ClientTick != 0 {
		t.Fatalf("server-owned bot ClientTick=%d, want 0", bot.ClientTick)
	}
	if bot.MoveDir == (simulation.Vector2{X: -99}) || !bot.PressedAttack {
		t.Fatalf("bot command must come from controller: %+v", bot)
	}
}

func TestMergedTickInputsDropsIneligibleBotPendingCommands(t *testing.T) {
	tests := []struct {
		name    string
		players []simulation.PlayerData
	}{
		{
			name: "dead bot",
			players: []simulation.PlayerData{
				{ID: "bot", Team: simulation.TeamRed, IsBot: true, IsDead: true},
				{ID: "enemy", Team: simulation.TeamBlue},
			},
		},
		{
			name: "bot without live target",
			players: []simulation.PlayerData{
				{ID: "bot", Team: simulation.TeamRed, IsBot: true},
				{ID: "ally", Team: simulation.TeamRed},
				{ID: "enemy-dead", Team: simulation.TeamBlue, IsDead: true},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mergedTickInputs(
				map[string]simulation.InputCommand{
					"bot": {PlayerID: "human", MoveDir: simulation.Vector2{X: -99}},
				},
				test.players,
			)
			if len(got) != 0 {
				t.Fatalf("ineligible bot pending command must be dropped, got %+v", got)
			}
		})
	}
}

func TestMergedTickInputsIsDeterministicSortedAndUnique(t *testing.T) {
	players, pending := authoritativeKeyFixture()
	playerPermutations := [][]simulation.PlayerData{
		players,
		{players[2], players[0], players[1]},
		{players[1], players[2], players[0]},
	}
	pendingPermutations := []map[string]simulation.InputCommand{
		pending,
		{
			"player-b": pending["player-b"],
			"player-c": pending["player-c"],
			"player-a": pending["player-a"],
		},
	}

	var want []simulation.InputCommand
	for playerIndex, playerOrder := range playerPermutations {
		for pendingIndex, pendingOrder := range pendingPermutations {
			got := mergedTickInputs(pendingOrder, playerOrder)
			if want == nil {
				want = got
			} else if !reflect.DeepEqual(got, want) {
				t.Fatalf("permutation players=%d pending=%d got %+v, want %+v", playerIndex, pendingIndex, got, want)
			}
			seen := make(map[simulation.PlayerID]struct{}, len(got))
			for _, input := range got {
				if _, duplicate := seen[input.PlayerID]; duplicate {
					t.Fatalf("duplicate PlayerID %q in %+v", input.PlayerID, got)
				}
				seen[input.PlayerID] = struct{}{}
			}
		}
	}
}

func TestBotBasicAttackUsesSharedSimulationChargeBudget(t *testing.T) {
	config, err := simulation.StaticGameConfig().SelectMode(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("select duel mode: %v", err)
	}
	playerConfig := config.DefaultPlayerType()
	if playerConfig.NormalAttack.MaxCharges != 3 || playerConfig.NormalAttack.RechargeTicks <= 5 {
		t.Fatalf("unexpected attack fixture: %+v", playerConfig)
	}
	view := []simulation.PlayerData{
		{ID: "bot", Team: simulation.TeamRed, Pos: simulation.Vector2{X: -1}, IsBot: true},
		{ID: "human", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 1}},
	}
	state := simulation.NewStateWithConfig(view, simulation.Config{Game: config})
	accepted := make([]bool, 0, 5)
	for range 5 {
		inputs := mergedTickInputs(nil, view)
		if len(inputs) != 1 || inputs[0].PlayerID != "bot" || !inputs[0].PressedAttack {
			t.Fatalf("bot must request one attack each tick: %+v", inputs)
		}
		snapshot := state.Step(inputs)
		accepted = append(accepted, playerByID(t, snapshot.Players, "bot").PressedAttack)
		view = append([]simulation.PlayerData(nil), snapshot.Players...)
	}
	want := []bool{true, true, true, false, false}
	if !reflect.DeepEqual(accepted, want) {
		t.Fatalf("shared simulation accepted=%v, want %v", accepted, want)
	}
}

func inputPlayerIDs(inputs []simulation.InputCommand) []simulation.PlayerID {
	ids := make([]simulation.PlayerID, len(inputs))
	for index, input := range inputs {
		ids[index] = input.PlayerID
	}
	return ids
}

func playerInputByID(
	t *testing.T,
	inputs []simulation.InputCommand,
	id simulation.PlayerID,
) simulation.InputCommand {
	t.Helper()
	for _, input := range inputs {
		if input.PlayerID == id {
			return input
		}
	}
	t.Fatalf("missing input %q in %+v", id, inputs)
	return simulation.InputCommand{}
}

func playerByID(
	t *testing.T,
	players []simulation.PlayerData,
	id simulation.PlayerID,
) simulation.PlayerData {
	t.Helper()
	for _, player := range players {
		if player.ID == id {
			return player
		}
	}
	t.Fatalf("missing player %q in %+v", id, players)
	return simulation.PlayerData{}
}

func TestNearestLiveEnemy(t *testing.T) {
	bot := simulation.PlayerData{
		ID:    "bot",
		Team:  simulation.TeamRed,
		Pos:   simulation.Vector2{X: 1, Y: 1},
		IsBot: true,
	}
	nearEnemy := simulation.PlayerData{
		ID:   "enemy-near",
		Team: simulation.TeamBlue,
		Pos:  simulation.Vector2{X: 4, Y: 5},
	}

	tests := []struct {
		name    string
		players []simulation.PlayerData
		wantID  simulation.PlayerID
		wantOK  bool
	}{
		{
			name: "self is excluded",
			players: []simulation.PlayerData{
				{ID: bot.ID, Team: simulation.TeamBlue, Pos: bot.Pos},
				nearEnemy,
			},
			wantID: nearEnemy.ID,
			wantOK: true,
		},
		{
			name: "ally is excluded",
			players: []simulation.PlayerData{
				bot,
				{ID: "ally", Team: simulation.TeamRed, Pos: simulation.Vector2{X: 1.1, Y: 1}},
				nearEnemy,
			},
			wantID: nearEnemy.ID,
			wantOK: true,
		},
		{
			name: "dead enemy is excluded",
			players: []simulation.PlayerData{
				bot,
				{ID: "enemy-dead", Team: simulation.TeamBlue, Pos: bot.Pos, IsDead: true},
				nearEnemy,
			},
			wantID: nearEnemy.ID,
			wantOK: true,
		},
		{
			name: "smallest squared distance wins",
			players: []simulation.PlayerData{
				bot,
				{ID: "enemy-far", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 7, Y: 1}},
				nearEnemy,
			},
			wantID: nearEnemy.ID,
			wantOK: true,
		},
		{
			name: "equal distance uses lexical ID when lexical winner is later",
			players: []simulation.PlayerData{
				bot,
				{ID: "enemy-z", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 2, Y: 1}},
				{ID: "enemy-a", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 0, Y: 1}},
			},
			wantID: "enemy-a",
			wantOK: true,
		},
		{
			name: "equal distance uses lexical ID when lexical winner is earlier",
			players: []simulation.PlayerData{
				{ID: "enemy-a", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 0, Y: 1}},
				bot,
				{ID: "enemy-z", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 2, Y: 1}},
			},
			wantID: "enemy-a",
			wantOK: true,
		},
		{
			name: "no live enemy",
			players: []simulation.PlayerData{
				bot,
				{ID: "ally", Team: simulation.TeamRed},
				{ID: "enemy-dead", Team: simulation.TeamBlue, IsDead: true},
			},
			wantOK: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := nearestLiveEnemy(bot, test.players)
			if ok != test.wantOK {
				t.Fatalf("nearestLiveEnemy() ok = %t, want %t; player=%+v", ok, test.wantOK, got)
			}
			if ok && got.ID != test.wantID {
				t.Fatalf("nearestLiveEnemy() ID = %q, want %q; player=%+v", got.ID, test.wantID, got)
			}
		})
	}
}

func TestBotInputForUsesUnitMovementAimAndBasicAttack(t *testing.T) {
	bot := simulation.PlayerData{
		ID: "bot", Team: simulation.TeamRed,
		Pos: simulation.Vector2{X: 1, Y: 1}, IsBot: true,
	}
	enemy := simulation.PlayerData{
		ID: "enemy", Team: simulation.TeamBlue,
		Pos: simulation.Vector2{X: 4, Y: 5},
	}
	got, ok := botInputFor(bot, []simulation.PlayerData{bot, enemy})
	if !ok {
		t.Fatal("expected bot input")
	}
	want := simulation.Vector2{X: 0.6, Y: 0.8}
	if got.PlayerID != bot.ID || got.MoveDir != want || got.AttackDir != want ||
		!got.PressedAttack {
		t.Fatalf("unexpected bot input: %+v", got)
	}
	if delta := math.Abs(math.Hypot(got.MoveDir.X, got.MoveDir.Y) - 1); delta > 1e-12 {
		t.Fatalf("MoveDir is not unit length: %+v", got.MoveDir)
	}
}

func TestBotInputForCoincidentEnemyUsesApprovedPositiveX(t *testing.T) {
	bot := simulation.PlayerData{ID: "bot", Team: simulation.TeamRed, IsBot: true}
	enemy := simulation.PlayerData{ID: "enemy", Team: simulation.TeamBlue}
	got, ok := botInputFor(bot, []simulation.PlayerData{bot, enemy})
	want := simulation.Vector2{X: 1, Y: 0}
	if !ok || got.PlayerID != bot.ID || got.MoveDir != want || got.AttackDir != want ||
		!got.PressedAttack {
		t.Fatalf("expected approved +X fallback, got %+v ok=%t", got, ok)
	}
}

func TestBotInputForRejectsIneligibleCallerOrMissingTarget(t *testing.T) {
	tests := []struct {
		name    string
		bot     simulation.PlayerData
		players []simulation.PlayerData
	}{
		{
			name: "non-bot caller",
			bot:  simulation.PlayerData{ID: "human", Team: simulation.TeamRed},
			players: []simulation.PlayerData{
				{ID: "enemy", Team: simulation.TeamBlue},
			},
		},
		{
			name: "dead bot",
			bot:  simulation.PlayerData{ID: "bot", Team: simulation.TeamRed, IsBot: true, IsDead: true},
			players: []simulation.PlayerData{
				{ID: "enemy", Team: simulation.TeamBlue},
			},
		},
		{
			name: "no live enemy",
			bot:  simulation.PlayerData{ID: "bot", Team: simulation.TeamRed, IsBot: true},
			players: []simulation.PlayerData{
				{ID: "bot", Team: simulation.TeamRed, IsBot: true},
				{ID: "ally", Team: simulation.TeamRed},
				{ID: "enemy-dead", Team: simulation.TeamBlue, IsDead: true},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := botInputFor(test.bot, test.players)
			if ok {
				t.Fatalf("botInputFor() = %+v, true; want false", got)
			}
			if got != (simulation.InputCommand{}) {
				t.Fatalf("botInputFor() rejected command = %+v, want zero value", got)
			}
		})
	}
}

func TestBotInputDeterministicAcrossPlayerOrder(t *testing.T) {
	bot := simulation.PlayerData{
		ID: "bot", Team: simulation.TeamRed, IsBot: true,
	}
	enemyA := simulation.PlayerData{
		ID: "enemy-a", Team: simulation.TeamBlue,
		Pos: simulation.Vector2{X: -1},
	}
	enemyZ := simulation.PlayerData{
		ID: "enemy-z", Team: simulation.TeamBlue,
		Pos: simulation.Vector2{X: 1},
	}
	permutations := [][]simulation.PlayerData{
		{bot, enemyA, enemyZ},
		{bot, enemyZ, enemyA},
		{enemyA, bot, enemyZ},
		{enemyA, enemyZ, bot},
		{enemyZ, bot, enemyA},
		{enemyZ, enemyA, bot},
	}

	var want simulation.InputCommand
	for index, players := range permutations {
		got, ok := botInputFor(bot, players)
		if !ok {
			t.Fatalf("permutation %d produced no input", index)
		}
		if index == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("permutation %d got %+v, want %+v", index, got, want)
		}
	}
	if want.AttackDir != (simulation.Vector2{X: -1}) {
		t.Fatalf("equal-distance tie must choose enemy-a, got %+v", want)
	}
}
