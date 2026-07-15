package rooms

import (
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestDuelGameEndResultsReturnNilWhenNoPlayersAreDead(t *testing.T) {
	results := calculateGameEndResults(simulation.StaticGameConfig(), simulation.Snapshot{
		Players: []simulation.PlayerData{
			{ID: "red"},
			{ID: "blue"},
		},
	})

	if results != nil {
		t.Fatalf("expected no GameEnd results without death, got %+v", results)
	}
}

func TestDuelGameEndResultsReturnWinLoseWhenOnePlayerIsDead(t *testing.T) {
	results := calculateGameEndResults(simulation.StaticGameConfig(), simulation.Snapshot{
		Players: []simulation.PlayerData{
			{ID: "red"},
			{ID: "blue", IsDead: true},
		},
	})

	assertGameEndResult(t, results, "red", gameEndResultWin)
	assertGameEndResult(t, results, "blue", gameEndResultLose)
}

func TestDuelGameEndResultsReturnDrawWhenBothPlayersAreDead(t *testing.T) {
	results := calculateGameEndResults(simulation.StaticGameConfig(), simulation.Snapshot{
		Players: []simulation.PlayerData{
			{ID: "red", IsDead: true},
			{ID: "blue", IsDead: true},
		},
	})

	assertGameEndResult(t, results, "red", gameEndResultDraw)
	assertGameEndResult(t, results, "blue", gameEndResultDraw)
}

func TestGameEndUsesRoomMode(t *testing.T) {
	mode := simulation.GameModeConfig{
		ID:              "custom_survival",
		PlayersPerMatch: 3,
		Teams: []simulation.TeamConfig{
			{Name: "custom-1", Size: 1},
			{Name: "custom-2", Size: 1},
			{Name: "custom-3", Size: 1},
		},
		Rules: simulation.GameModeRulesConfig{
			TeamBehavior: simulation.TeamBehaviorFreeForAll,
			FriendlyFire: false,
		},
	}
	roomConfig := singleModeGameConfig(mode)
	room := &room{gameConfig: roomConfig}
	results := room.gameEndResults(simulation.Snapshot{
		Players: []simulation.PlayerData{
			{ID: "custom-1"},
			{ID: "custom-2", IsDead: true},
			{ID: "custom-3"},
		},
	})

	assertGameEndResult(t, results, "custom-1", gameEndResultWin)
	assertGameEndResult(t, results, "custom-2", gameEndResultLose)
	assertGameEndResult(t, results, "custom-3", gameEndResultWin)
}

func assertGameEndResult(t *testing.T, results map[string]gameEndResult, playerID string, want gameEndResult) {
	t.Helper()

	got, ok := results[playerID]
	if !ok {
		t.Fatalf("expected GameEnd result for player %s, got %+v", playerID, results)
	}
	if got != want {
		t.Fatalf("expected player %s result %q, got %q", playerID, want, got)
	}
}
