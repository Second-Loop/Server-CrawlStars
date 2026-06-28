package simulation

import (
	"encoding/json"
	"os"
	"path/filepath"
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

type clientSharedGameConfig struct {
	Version          int      `json:"version"`
	TileSize         float64  `json:"tileSize"`
	PlayerRadius     float64  `json:"playerRadius"`
	PlayerTypes      []string `json:"playerTypes"`
	ProjectileRadius float64  `json:"projectileRadius"`
	ProjectileTypes  []string `json:"projectileTypes"`
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
