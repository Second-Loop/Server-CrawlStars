package rooms

import (
	"reflect"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestSoloGameEndDecisionEdges(t *testing.T) {
	config := selectedGameEndMode(t, simulation.GameModeSolo)
	players := []simulation.PlayerData{
		{ID: "solo-1", Team: "solo-1"},
		{ID: "solo-2", Team: "solo-2"},
		{ID: "solo-3", Team: "solo-3"},
		{ID: "solo-4", Team: "solo-4"},
		{ID: "solo-5", Team: "solo-5"},
		{ID: "solo-6", Team: "solo-6"},
	}

	tests := []struct {
		name     string
		players  []simulation.PlayerData
		want     map[string]gameEndResult
		terminal bool
	}{
		{name: "empty"},
		{name: "initial one alive", players: players[:1]},
		{name: "six alive", players: players},
		{
			name:    "one intermediate death",
			players: playersWithDeaths(players, "solo-1"),
			want:    map[string]gameEndResult{"solo-1": gameEndResultLose},
		},
		{
			name:    "last survivor",
			players: playersWithDeaths(players, "solo-1", "solo-2", "solo-3", "solo-4", "solo-5"),
			want: map[string]gameEndResult{
				"solo-1": gameEndResultLose,
				"solo-2": gameEndResultLose,
				"solo-3": gameEndResultLose,
				"solo-4": gameEndResultLose,
				"solo-5": gameEndResultLose,
				"solo-6": gameEndResultWin,
			},
			terminal: true,
		},
		{
			name:    "first observed all dead",
			players: playersWithDeaths(players, "solo-1", "solo-2", "solo-3", "solo-4", "solo-5", "solo-6"),
			want: map[string]gameEndResult{
				"solo-1": gameEndResultDraw,
				"solo-2": gameEndResultDraw,
				"solo-3": gameEndResultDraw,
				"solo-4": gameEndResultDraw,
				"solo-5": gameEndResultDraw,
				"solo-6": gameEndResultDraw,
			},
			terminal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGameEndDecision(t, config, simulation.Snapshot{Players: tt.players}, tt.want, tt.terminal)
		})
	}
}

func TestTeamGameEndRules(t *testing.T) {
	config := selectedGameEndMode(t, simulation.GameModeTeam)
	players := []simulation.PlayerData{
		{ID: "red-1", Team: simulation.TeamRed},
		{ID: "blue-1", Team: simulation.TeamBlue},
		{ID: "red-2", Team: simulation.TeamRed},
		{ID: "blue-2", Team: simulation.TeamBlue},
		{ID: "red-3", Team: simulation.TeamRed},
		{ID: "blue-3", Team: simulation.TeamBlue},
	}

	tests := []struct {
		name     string
		players  []simulation.PlayerData
		want     map[string]gameEndResult
		terminal bool
	}{
		{
			name:    "partial red death",
			players: playersWithDeaths(players, "red-1"),
		},
		{
			name:    "red eliminated",
			players: playersWithDeaths(players, "red-1", "red-2", "red-3", "blue-1"),
			want: map[string]gameEndResult{
				"red-1":  gameEndResultLose,
				"blue-1": gameEndResultWin,
				"red-2":  gameEndResultLose,
				"blue-2": gameEndResultWin,
				"red-3":  gameEndResultLose,
				"blue-3": gameEndResultWin,
			},
			terminal: true,
		},
		{
			name:    "both teams eliminated",
			players: playersWithDeaths(players, "red-1", "blue-1", "red-2", "blue-2", "red-3", "blue-3"),
			want: map[string]gameEndResult{
				"red-1":  gameEndResultDraw,
				"blue-1": gameEndResultDraw,
				"red-2":  gameEndResultDraw,
				"blue-2": gameEndResultDraw,
				"red-3":  gameEndResultDraw,
				"blue-3": gameEndResultDraw,
			},
			terminal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGameEndDecision(t, config, simulation.Snapshot{Players: tt.players}, tt.want, tt.terminal)
		})
	}
}

func TestDuelGameEndRulesRemainCompatible(t *testing.T) {
	config := selectedGameEndMode(t, simulation.GameModeDuel1v1)
	players := []simulation.PlayerData{
		{ID: "red", Team: simulation.TeamRed},
		{ID: "blue", Team: simulation.TeamBlue},
	}

	tests := []struct {
		name     string
		players  []simulation.PlayerData
		want     map[string]gameEndResult
		terminal bool
	}{
		{name: "no death", players: players},
		{
			name:    "one death",
			players: playersWithDeaths(players, "blue"),
			want: map[string]gameEndResult{
				"red":  gameEndResultWin,
				"blue": gameEndResultLose,
			},
			terminal: true,
		},
		{
			name:    "both dead",
			players: playersWithDeaths(players, "red", "blue"),
			want: map[string]gameEndResult{
				"red":  gameEndResultDraw,
				"blue": gameEndResultDraw,
			},
			terminal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGameEndDecision(t, config, simulation.Snapshot{Players: tt.players}, tt.want, tt.terminal)
		})
	}
}

func TestCustomModeFallsBackToPlayerSurvival(t *testing.T) {
	config := simulation.StaticGameConfig()
	config.SelectedMode = simulation.GameModeConfig{
		ID:              "custom_survival",
		PlayersPerMatch: 3,
		Teams: []simulation.TeamConfig{
			{Name: "custom-1", Size: 1},
			{Name: "custom-2", Size: 1},
			{Name: "custom-3", Size: 1},
		},
		Rules: simulation.GameModeRulesConfig{
			TeamBehavior: simulation.TeamBehaviorFreeForAll,
		},
	}
	players := []simulation.PlayerData{
		{ID: "custom-1", Team: "custom-1"},
		{ID: "custom-2", Team: "custom-2"},
		{ID: "custom-3", Team: "custom-3"},
	}

	assertGameEndDecision(
		t,
		config,
		simulation.Snapshot{Players: playersWithDeaths(players, "custom-2")},
		map[string]gameEndResult{
			"custom-1": gameEndResultWin,
			"custom-2": gameEndResultLose,
			"custom-3": gameEndResultWin,
		},
		true,
	)
}

func TestRoomClaimsFinalizedGameEndResultOnlyOnce(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	room := store.lookupRoom(created.ID)

	room.mu.Lock()
	firstClaim := room.claimFinalizedGameEndResults(map[string]gameEndResult{
		"player-1": gameEndResultLose,
	})
	secondClaim := room.claimFinalizedGameEndResults(map[string]gameEndResult{
		"player-1": gameEndResultDraw,
		"player-2": gameEndResultWin,
	})
	ledger := make(map[string]gameEndResult, len(room.finalizedGameEndResults))
	for playerID, result := range room.finalizedGameEndResults {
		ledger[playerID] = result
	}
	room.mu.Unlock()

	if want := map[string]gameEndResult{"player-1": gameEndResultLose}; !reflect.DeepEqual(firstClaim, want) {
		t.Fatalf("expected first claim %+v, got %+v", want, firstClaim)
	}
	if want := map[string]gameEndResult{"player-2": gameEndResultWin}; !reflect.DeepEqual(secondClaim, want) {
		t.Fatalf("expected only the new player claim %+v, got %+v", want, secondClaim)
	}
	if want := map[string]gameEndResult{
		"player-1": gameEndResultLose,
		"player-2": gameEndResultWin,
	}; !reflect.DeepEqual(ledger, want) {
		t.Fatalf("expected immutable finalized result ledger %+v, got %+v", want, ledger)
	}
}

func TestRoomSignalsGameEndCleanupOnlyOnce(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	room := store.lookupRoom(created.ID)
	if room.gameEndCleanupDone == nil {
		t.Fatal("expected new room to initialize GameEnd cleanup completion")
	}

	room.signalGameEndCleanupDone()
	room.signalGameEndCleanupDone()

	select {
	case <-room.gameEndCleanupDone:
	default:
		t.Fatal("expected GameEnd cleanup completion to be closed")
	}
}

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

	results := room.calculateGameEndResults(snapshot)

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

func selectedGameEndMode(t *testing.T, modeID string) simulation.GameConfig {
	t.Helper()
	config, err := simulation.StaticGameConfig().SelectMode(modeID)
	if err != nil {
		t.Fatalf("select mode %q: %v", modeID, err)
	}
	return config
}

func playersWithDeaths(players []simulation.PlayerData, deadIDs ...simulation.PlayerID) []simulation.PlayerData {
	dead := make(map[simulation.PlayerID]bool, len(deadIDs))
	for _, id := range deadIDs {
		dead[id] = true
	}
	result := append([]simulation.PlayerData(nil), players...)
	for i := range result {
		if dead[result[i].ID] {
			result[i].IsDead = true
			result[i].HP = 0
		}
	}
	return result
}

func assertGameEndDecision(t *testing.T, config simulation.GameConfig, snapshot simulation.Snapshot, want map[string]gameEndResult, terminal bool) {
	t.Helper()
	var got map[string]gameEndResult
	switch config.SelectedMode.ID {
	case simulation.GameModeSolo:
		got = soloGameEndResults(snapshot.Players)
	case simulation.GameModeTeam:
		got = teamGameEndResults(config.SelectedMode, snapshot.Players)
	case simulation.GameModeDuel1v1:
		got = duelGameEndResults(snapshot.Players)
	default:
		got = playerSurvivalGameEndResults(snapshot.Players)
	}
	if !reflect.DeepEqual(got, want) || shouldEndGame(config, snapshot) != terminal {
		t.Fatalf("got results=%+v terminal=%t, want results=%+v terminal=%t", got, shouldEndGame(config, snapshot), want, terminal)
	}
}
