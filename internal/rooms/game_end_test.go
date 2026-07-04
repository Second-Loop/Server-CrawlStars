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
