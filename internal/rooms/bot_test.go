package rooms

import (
	"math"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

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
