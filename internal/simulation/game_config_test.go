package simulation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientGameConfigArtifactOnlyIncludesSharedClientConstants(t *testing.T) {
	config := loadClientSharedGameConfig(t)

	if config.Version != 1 {
		t.Fatalf("expected client config version 1, got %d", config.Version)
	}
	if config.TileSize != TileSize {
		t.Fatalf("expected tile size %f, got %f", TileSize, config.TileSize)
	}
	if config.PlayerRadius != DefaultPlayerRadius {
		t.Fatalf("expected player radius %f, got %f", DefaultPlayerRadius, config.PlayerRadius)
	}
	if len(config.PlayerTypes) != 1 || config.PlayerTypes[0] != "default" {
		t.Fatalf("expected default player type list, got %+v", config.PlayerTypes)
	}
	if config.ProjectileRadius != DefaultProjectileRadius {
		t.Fatalf("expected projectile radius %f, got %f", DefaultProjectileRadius, config.ProjectileRadius)
	}
	if len(config.ProjectileTypes) != 1 || config.ProjectileTypes[0] != "default" {
		t.Fatalf("expected default projectile type list, got %+v", config.ProjectileTypes)
	}
	if config.ContainsServerMode {
		t.Fatal("client config must not include server-only mode rules")
	}
}

func TestServerGameConfigArtifactMatchesServerSimulationConstants(t *testing.T) {
	config := loadServerGameConfig(t)

	if config.Version != 1 {
		t.Fatalf("expected server config version 1, got %d", config.Version)
	}
	if config.TickRate != TickRate {
		t.Fatalf("expected tick rate %d, got %d", TickRate, config.TickRate)
	}
	if config.Tile.Size != TileSize {
		t.Fatalf("expected tile size %f, got %f", TileSize, config.Tile.Size)
	}
	if len(config.Player.Types) != 1 {
		t.Fatalf("expected one player type, got %+v", config.Player.Types)
	}
	if config.Player.Types[0].ID != "default" {
		t.Fatalf("expected default player type, got %+v", config.Player.Types[0])
	}
	if config.Player.Types[0].Radius != DefaultPlayerRadius {
		t.Fatalf("expected player radius %f, got %f", DefaultPlayerRadius, config.Player.Types[0].Radius)
	}
	if config.Player.Types[0].HP != DefaultPlayerHP {
		t.Fatalf("expected player HP %f, got %f", DefaultPlayerHP, config.Player.Types[0].HP)
	}
	if config.Player.Types[0].Speed != DefaultPlayerSpeed {
		t.Fatalf("expected player speed %f, got %f", DefaultPlayerSpeed, config.Player.Types[0].Speed)
	}
	if len(config.Projectile.Types) != 1 {
		t.Fatalf("expected one projectile type, got %+v", config.Projectile.Types)
	}
	if config.Projectile.Types[0].ID != "default" {
		t.Fatalf("expected default projectile type, got %+v", config.Projectile.Types[0])
	}
	if config.Projectile.Types[0].Radius != DefaultProjectileRadius {
		t.Fatalf("expected projectile radius %f, got %f", DefaultProjectileRadius, config.Projectile.Types[0].Radius)
	}
	if config.Projectile.Types[0].Damage != DefaultProjectileDamage {
		t.Fatalf("expected projectile damage %f, got %f", DefaultProjectileDamage, config.Projectile.Types[0].Damage)
	}
	if config.Projectile.Types[0].Speed != DefaultProjectileSpeed {
		t.Fatalf("expected projectile speed %f, got %f", DefaultProjectileSpeed, config.Projectile.Types[0].Speed)
	}
}

func TestServerGameConfigArtifactIncludesDefaultOneVsOneMode(t *testing.T) {
	config := loadServerGameConfig(t)

	if config.Mode.ID != GameModeDuel1v1 {
		t.Fatalf("expected default mode %q, got %q", GameModeDuel1v1, config.Mode.ID)
	}
	if config.Mode.PlayersPerMatch != 2 {
		t.Fatalf("expected default mode playersPerMatch 2, got %d", config.Mode.PlayersPerMatch)
	}
	if len(config.Mode.Teams) != 2 {
		t.Fatalf("expected red/blue teams, got %+v", config.Mode.Teams)
	}
	if config.Mode.Teams[0] != (TeamConfig{Name: TeamRed, Size: 1}) {
		t.Fatalf("expected first team red size 1, got %+v", config.Mode.Teams[0])
	}
	if config.Mode.Teams[1] != (TeamConfig{Name: TeamBlue, Size: 1}) {
		t.Fatalf("expected second team blue size 1, got %+v", config.Mode.Teams[1])
	}
	if config.Mode.Rules.TeamBehavior != TeamBehaviorTwoTeams {
		t.Fatalf("expected team behavior %q, got %q", TeamBehaviorTwoTeams, config.Mode.Rules.TeamBehavior)
	}
	if config.Mode.Rules.FriendlyFire {
		t.Fatal("expected default 1v1 mode to disable friendly fire")
	}
}

func TestServerGameConfigArtifactIncludesRuntimeMap(t *testing.T) {
	config := loadServerGameConfig(t)
	gameMap, err := ResolveMapData(config.Map)
	if err != nil {
		t.Fatalf("resolve server game config map: %v", err)
	}

	if gameMap.Width != 20 || gameMap.Height != 20 {
		t.Fatalf("expected 20x20 runtime map, got %dx%d", gameMap.Width, gameMap.Height)
	}
	if gameMap.MaxPlayers != 6 {
		t.Fatalf("expected map maxPlayers 6, got %d", gameMap.MaxPlayers)
	}
	if gameMap.TileSize != TileSize {
		t.Fatalf("expected map tile size %f, got %f", TileSize, gameMap.TileSize)
	}
}

func TestResolveGameConfigRejectsInvalidModeRules(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*GameConfig)
		wantErr string
	}{
		{
			name: "empty mode id",
			mutate: func(config *GameConfig) {
				config.Mode.ID = ""
			},
			wantErr: "mode.id",
		},
		{
			name: "empty team name",
			mutate: func(config *GameConfig) {
				config.Mode.Teams[0].Name = ""
			},
			wantErr: "team name",
		},
		{
			name: "team size sum mismatch",
			mutate: func(config *GameConfig) {
				config.Mode.Teams[0].Size = 2
			},
			wantErr: "team size total",
		},
		{
			name: "mode exceeds map capacity",
			mutate: func(config *GameConfig) {
				config.Map.MaxPlayers = 1
			},
			wantErr: "map.maxPlayers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := StaticGameConfig()
			tt.mutate(&config)

			_, err := ResolveGameConfig(config)
			if err == nil {
				t.Fatal("expected config to be rejected")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error to contain %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestGameConfigAssignsDefaultOneVsOneMatchTeams(t *testing.T) {
	config := StaticGameConfig()

	team, slot, ok := config.MatchTeamForPlayerIndex(0)
	if !ok || team != TeamRed || slot != 0 {
		t.Fatalf("expected player index 0 to be red slot 0, got team=%q slot=%d ok=%v", team, slot, ok)
	}
	team, slot, ok = config.MatchTeamForPlayerIndex(1)
	if !ok || team != TeamBlue || slot != 0 {
		t.Fatalf("expected player index 1 to be blue slot 0, got team=%q slot=%d ok=%v", team, slot, ok)
	}
	if team, slot, ok = config.MatchTeamForPlayerIndex(2); ok {
		t.Fatalf("expected player index 2 to be outside active 1v1 match, got team=%q slot=%d", team, slot)
	}
}

func TestGameConfigAssignsConfiguredMatchTeams(t *testing.T) {
	config := StaticGameConfig()
	config.Mode = GameModeConfig{
		ID:              "test_quartet",
		PlayersPerMatch: 4,
		Teams: []TeamConfig{
			{Name: TeamRed, Size: 3},
			{Name: TeamBlue, Size: 1},
		},
		Rules: GameModeRulesConfig{
			TeamBehavior: TeamBehaviorTwoTeams,
			FriendlyFire: false,
		},
	}

	tests := []struct {
		index int
		team  Team
		slot  int
	}{
		{index: 0, team: TeamRed, slot: 0},
		{index: 1, team: TeamBlue, slot: 0},
		{index: 2, team: TeamRed, slot: 1},
		{index: 3, team: TeamRed, slot: 2},
	}
	for _, tt := range tests {
		team, slot, ok := config.MatchTeamForPlayerIndex(tt.index)
		if !ok || team != tt.team || slot != tt.slot {
			t.Fatalf("expected player index %d to be %s slot %d, got team=%q slot=%d ok=%v", tt.index, tt.team, tt.slot, team, slot, ok)
		}
	}

	if team, slot, ok := config.MatchTeamForPlayerIndex(4); ok {
		t.Fatalf("expected player index 4 to be outside active match, got team=%q slot=%d", team, slot)
	}
}

type clientSharedGameConfig struct {
	Version            int      `json:"version"`
	TileSize           float64  `json:"tileSize"`
	PlayerRadius       float64  `json:"playerRadius"`
	PlayerTypes        []string `json:"playerTypes"`
	ProjectileRadius   float64  `json:"projectileRadius"`
	ProjectileTypes    []string `json:"projectileTypes"`
	ContainsServerMode bool     `json:"mode"`
}

func loadClientSharedGameConfig(t *testing.T) clientSharedGameConfig {
	t.Helper()

	path := filepath.Join("..", "..", "client-config", "game-config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode %s keys: %v", path, err)
	}
	wantKeys := map[string]bool{
		"version":          true,
		"tileSize":         true,
		"playerRadius":     true,
		"playerTypes":      true,
		"projectileRadius": true,
		"projectileTypes":  true,
	}
	for key := range raw {
		if !wantKeys[key] {
			t.Fatalf("client config must not include server-only key %q", key)
		}
	}
	for key := range wantKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("client config missing shared key %q", key)
		}
	}

	var config clientSharedGameConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return config
}

func loadServerGameConfig(t *testing.T) GameConfig {
	t.Helper()

	path := filepath.Join("..", "..", "server-config", "game-config.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()

	config, err := LoadGameConfig(file)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return config
}
