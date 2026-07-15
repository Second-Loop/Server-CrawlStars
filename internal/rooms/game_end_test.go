package rooms

import (
	"reflect"
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

func TestGameEndUsesRoomConfig(t *testing.T) {
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
	snapshot := simulation.Snapshot{
		Players: []simulation.PlayerData{
			{ID: "custom-1"},
			{ID: "custom-2", IsDead: true},
			{ID: "custom-3"},
		},
	}
	wantResults := map[string]gameEndResult{"captured": gameEndResultDraw}
	var capturedConfig simulation.GameConfig
	var capturedSnapshot simulation.Snapshot
	room := &room{
		gameConfig: roomConfig,
		calculateGameEnd: func(gameConfig simulation.GameConfig, snapshot simulation.Snapshot) map[string]gameEndResult {
			capturedConfig = gameConfig
			capturedSnapshot = snapshot
			return wantResults
		},
	}

	results := room.gameEndResults(snapshot)

	if !reflect.DeepEqual(capturedConfig, roomConfig) {
		t.Fatalf("expected calculator to receive room config %+v, got %+v", roomConfig, capturedConfig)
	}
	if capturedConfig.SelectedMode.ID != mode.ID {
		t.Fatalf("expected calculator to receive selected mode %q, got %q", mode.ID, capturedConfig.SelectedMode.ID)
	}
	if !reflect.DeepEqual(capturedSnapshot, snapshot) {
		t.Fatalf("expected calculator to receive snapshot %+v, got %+v", snapshot, capturedSnapshot)
	}
	if !reflect.DeepEqual(results, wantResults) {
		t.Fatalf("expected calculator results %+v, got %+v", wantResults, results)
	}
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
