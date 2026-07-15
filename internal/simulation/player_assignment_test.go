package simulation

import (
	"math"
	"testing"
)

func TestPlayerAssignmentsUseMapSpawnPointTilesInJoinOrder(t *testing.T) {
	config := assignmentTestConfig()
	config.SelectedMode = GameModeConfig{
		ID:              "test_trio",
		PlayersPerMatch: 3,
		Teams: []TeamConfig{
			{Name: TeamRed, Size: 2},
			{Name: TeamBlue, Size: 1},
		},
		Rules: GameModeRulesConfig{
			TeamBehavior: TeamBehaviorTwoTeams,
			FriendlyFire: false,
		},
	}
	config.Map = MapData{
		Width:      6,
		Height:     4,
		Index:      2,
		MaxPlayers: 3,
		TileSize:   TileSize,
		Map: [][]TileType{
			{TileWall, TileWall, TileWall, TileWall, TileWall, TileWall},
			{TileWall, TileGround, TileSpawnPoint, TileGround, TileSpawnPoint, TileWall},
			{TileWall, TileGround, TileGround, TileSpawnPoint, TileGround, TileWall},
			{TileWall, TileWall, TileWall, TileWall, TileWall, TileWall},
		},
	}

	assignments := PlayerAssignments([]PlayerID{"player-1", "player-2", "player-3"}, config)

	assertPlayerAssignment(t, assignments, 0, PlayerAssignment{
		ID:            "player-1",
		Team:          TeamRed,
		Slot:          0,
		SpawnPosition: config.Map.WorldPos(2, 1),
	})
	assertPlayerAssignment(t, assignments, 1, PlayerAssignment{
		ID:            "player-2",
		Team:          TeamBlue,
		Slot:          0,
		SpawnPosition: config.Map.WorldPos(4, 1),
	})
	assertPlayerAssignment(t, assignments, 2, PlayerAssignment{
		ID:            "player-3",
		Team:          TeamRed,
		Slot:          1,
		SpawnPosition: config.Map.WorldPos(3, 2),
	})
}

func TestPlayerAssignmentsUseSelectedModeTeams(t *testing.T) {
	playerIDs := []PlayerID{"player-1", "player-2", "player-3", "player-4", "player-5", "player-6"}
	tests := []struct {
		modeID string
		teams  []Team
		slots  []int
	}{
		{
			modeID: GameModeTeam,
			teams:  []Team{TeamRed, TeamBlue, TeamRed, TeamBlue, TeamRed, TeamBlue},
			slots:  []int{0, 0, 1, 1, 2, 2},
		},
		{
			modeID: GameModeSolo,
			teams:  []Team{Team("solo-1"), Team("solo-2"), Team("solo-3"), Team("solo-4"), Team("solo-5"), Team("solo-6")},
			slots:  []int{0, 0, 0, 0, 0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.modeID, func(t *testing.T) {
			config, err := assignmentTestConfig().SelectMode(tt.modeID)
			if err != nil {
				t.Fatalf("select mode %q: %v", tt.modeID, err)
			}

			assignments := PlayerAssignments(playerIDs, config)
			for index := range playerIDs {
				if assignments[index].Team != tt.teams[index] || assignments[index].Slot != tt.slots[index] {
					t.Fatalf("player index %d: expected team=%q slot=%d, got team=%q slot=%d", index, tt.teams[index], tt.slots[index], assignments[index].Team, assignments[index].Slot)
				}
			}
		})
	}
}

func TestPlayerAssignmentsUseMapDerivedFallbackSpawnsWhenNoSpawnTiles(t *testing.T) {
	config := assignmentTestConfig()
	config.Map = noSpawnAssignmentMap()

	assignments := PlayerAssignments([]PlayerID{"player-1", "player-2"}, config)

	assertPlayerAssignment(t, assignments, 0, PlayerAssignment{
		ID:            "player-1",
		Team:          TeamRed,
		Slot:          0,
		SpawnPosition: config.Map.WorldPos(1, 1),
	})
	assertPlayerAssignment(t, assignments, 1, PlayerAssignment{
		ID:            "player-2",
		Team:          TeamBlue,
		Slot:          0,
		SpawnPosition: config.Map.WorldPos(config.Map.Width-2, config.Map.Height-2),
	})
}

func TestPlayerAssignmentsSkipBlockingFallbackTiles(t *testing.T) {
	config := assignmentTestConfig()
	config.Map = noSpawnAssignmentMap()
	config.Map.Map[1][1] = TileWater
	config.Map.Map[config.Map.Height-2][config.Map.Width-2] = TileWall

	assignments := PlayerAssignments([]PlayerID{"player-1"}, config)

	assertPlayerAssignment(t, assignments, 0, PlayerAssignment{
		ID:            "player-1",
		Team:          TeamRed,
		Slot:          0,
		SpawnPosition: config.Map.WorldPos(config.Map.Width-2, 1),
	})
}

func TestPlayerAssignmentsUseFallbackOnlyAfterSpawnPointsAreExhausted(t *testing.T) {
	config := assignmentTestConfig()
	config.Map = noSpawnAssignmentMap()
	config.Map.Map[1][2] = TileSpawnPoint

	assignments := PlayerAssignments([]PlayerID{"player-1", "player-2"}, config)

	assertPlayerAssignment(t, assignments, 0, PlayerAssignment{
		ID:            "player-1",
		Team:          TeamRed,
		Slot:          0,
		SpawnPosition: config.Map.WorldPos(2, 1),
	})
	assertPlayerAssignment(t, assignments, 1, PlayerAssignment{
		ID:            "player-2",
		Team:          TeamBlue,
		Slot:          0,
		SpawnPosition: config.Map.WorldPos(1, 1),
	})
}

func TestPlayerAssignmentsAvoidFallbackTilesAlreadyUsedBySpawnPoints(t *testing.T) {
	config := assignmentTestConfig()
	config.Map = noSpawnAssignmentMap()
	config.Map.Map[2][config.Map.Width-2] = TileSpawnPoint

	assignments := PlayerAssignments([]PlayerID{"player-1", "player-2"}, config)

	assertPlayerAssignment(t, assignments, 0, PlayerAssignment{
		ID:            "player-1",
		Team:          TeamRed,
		Slot:          0,
		SpawnPosition: config.Map.WorldPos(config.Map.Width-2, config.Map.Height-2),
	})
	assertPlayerAssignment(t, assignments, 1, PlayerAssignment{
		ID:            "player-2",
		Team:          TeamBlue,
		Slot:          0,
		SpawnPosition: config.Map.WorldPos(1, 1),
	})
}

func assignmentTestConfig() GameConfig {
	config := StaticGameConfig()
	config.Map = noSpawnAssignmentMap()
	return config
}

func noSpawnAssignmentMap() MapData {
	return MapData{
		Width:      7,
		Height:     4,
		Index:      3,
		MaxPlayers: 6,
		TileSize:   TileSize,
		Map: [][]TileType{
			{TileWall, TileWall, TileWall, TileWall, TileWall, TileWall, TileWall},
			{TileWall, TileGround, TileGround, TileGround, TileGround, TileGround, TileWall},
			{TileWall, TileGround, TileGround, TileGround, TileGround, TileGround, TileWall},
			{TileWall, TileWall, TileWall, TileWall, TileWall, TileWall, TileWall},
		},
	}
}

func assertPlayerAssignment(t *testing.T, assignments []PlayerAssignment, index int, want PlayerAssignment) {
	t.Helper()

	if len(assignments) <= index {
		t.Fatalf("expected assignment index %d in %+v", index, assignments)
	}
	got := assignments[index]
	if got.ID != want.ID || got.Team != want.Team || got.Slot != want.Slot || !sameVector(got.SpawnPosition, want.SpawnPosition) {
		t.Fatalf("expected assignment index %d %+v, got %+v", index, want, got)
	}
}

func sameVector(a Vector2, b Vector2) bool {
	return math.Abs(a.X-b.X) < 0.000001 && math.Abs(a.Y-b.Y) < 0.000001
}
