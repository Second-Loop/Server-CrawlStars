package simulation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGameConfigArtifactMatchesServerSimulationConstants(t *testing.T) {
	config := loadClientGameConfig(t)

	if config.Version != 1 {
		t.Fatalf("expected client config version 1, got %d", config.Version)
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

func TestGameConfigArtifactIncludesRuntimeMap(t *testing.T) {
	config := loadClientGameConfig(t)
	gameMap, err := ResolveMapData(config.Map)
	if err != nil {
		t.Fatalf("resolve game config map: %v", err)
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

func loadClientGameConfig(t *testing.T) GameConfig {
	t.Helper()

	path := filepath.Join("..", "..", "client-config", "game-config.json")
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
