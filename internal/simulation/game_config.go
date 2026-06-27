package simulation

import (
	"encoding/json"
	"fmt"
	"io"
)

type GameConfig struct {
	Version    int                     `json:"version"`
	TickRate   int                     `json:"tickRate"`
	Tile       TileConfig              `json:"tile"`
	Player     PlayerTypeSetConfig     `json:"player"`
	Projectile ProjectileTypeSetConfig `json:"projectile"`
	Map        MapData                 `json:"map"`
}

type TileConfig struct {
	Size float64 `json:"size"`
}

type PlayerTypeSetConfig struct {
	Types []PlayerTypeConfig `json:"types"`
}

type PlayerTypeConfig struct {
	ID     string  `json:"id"`
	Radius float64 `json:"radius"`
	HP     float64 `json:"hp"`
	Speed  float64 `json:"speed"`
}

type ProjectileTypeSetConfig struct {
	Types []ProjectileTypeConfig `json:"types"`
}

type ProjectileTypeConfig struct {
	ID     string  `json:"id"`
	Radius float64 `json:"radius"`
	Damage float64 `json:"damage"`
	Speed  float64 `json:"speed"`
}

func LoadGameConfig(reader io.Reader) (GameConfig, error) {
	var config GameConfig
	if err := json.NewDecoder(reader).Decode(&config); err != nil {
		return GameConfig{}, fmt.Errorf("decode game config: %w", err)
	}
	return ResolveGameConfig(config)
}

func ResolveGameConfig(config GameConfig) (GameConfig, error) {
	if config.Version <= 0 {
		return GameConfig{}, fmt.Errorf("game config version must be positive")
	}
	if config.TickRate <= 0 {
		return GameConfig{}, fmt.Errorf("game config tickRate must be positive")
	}
	if config.Tile.Size <= 0 {
		return GameConfig{}, fmt.Errorf("game config tile.size must be positive")
	}
	if len(config.Player.Types) == 0 {
		return GameConfig{}, fmt.Errorf("game config player.types must not be empty")
	}
	for _, player := range config.Player.Types {
		if player.ID == "" {
			return GameConfig{}, fmt.Errorf("game config player type id must not be empty")
		}
		if player.Radius <= 0 || player.HP <= 0 || player.Speed <= 0 {
			return GameConfig{}, fmt.Errorf("game config player type %q values must be positive", player.ID)
		}
	}
	if len(config.Projectile.Types) == 0 {
		return GameConfig{}, fmt.Errorf("game config projectile.types must not be empty")
	}
	for _, projectile := range config.Projectile.Types {
		if projectile.ID == "" {
			return GameConfig{}, fmt.Errorf("game config projectile type id must not be empty")
		}
		if projectile.Radius <= 0 || projectile.Damage <= 0 || projectile.Speed <= 0 {
			return GameConfig{}, fmt.Errorf("game config projectile type %q values must be positive", projectile.ID)
		}
	}
	gameMap := config.Map
	if gameMap.TileSize <= 0 {
		gameMap.TileSize = config.Tile.Size
	}
	resolvedMap, err := ResolveMapData(gameMap)
	if err != nil {
		return GameConfig{}, fmt.Errorf("resolve game config map: %w", err)
	}
	config.Map = resolvedMap
	return config, nil
}

func StaticGameConfig() GameConfig {
	return GameConfig{
		Version:  1,
		TickRate: TickRate,
		Tile: TileConfig{
			Size: TileSize,
		},
		Player: PlayerTypeSetConfig{
			Types: []PlayerTypeConfig{
				{
					ID:     "default",
					Radius: DefaultPlayerRadius,
					HP:     DefaultPlayerHP,
					Speed:  DefaultPlayerSpeed,
				},
			},
		},
		Projectile: ProjectileTypeSetConfig{
			Types: []ProjectileTypeConfig{
				{
					ID:     "default",
					Radius: DefaultProjectileRadius,
					Damage: DefaultProjectileDamage,
					Speed:  DefaultProjectileSpeed,
				},
			},
		},
		Map: StaticMapFixture(),
	}
}

func (config GameConfig) DefaultPlayerType() PlayerTypeConfig {
	if len(config.Player.Types) == 0 {
		return StaticGameConfig().Player.Types[0]
	}
	return config.Player.Types[0]
}

func (config GameConfig) DefaultProjectileType() ProjectileTypeConfig {
	if len(config.Projectile.Types) == 0 {
		return StaticGameConfig().Projectile.Types[0]
	}
	return config.Projectile.Types[0]
}

func resolveStateGameConfig(config Config) GameConfig {
	gameConfig := config.Game
	hasConfigMap := config.Map.Width > 0 || config.Map.Height > 0 || len(config.Map.Map) > 0
	if gameConfig.Version <= 0 {
		gameConfig = StaticGameConfig()
		if !hasConfigMap {
			gameConfig.Map = MapData{}
		}
	}
	if hasConfigMap {
		gameConfig.Map = config.Map
	}
	if gameConfig.Map.Width > 0 || gameConfig.Map.Height > 0 || len(gameConfig.Map.Map) > 0 {
		resolved, err := ResolveGameConfig(gameConfig)
		if err != nil {
			return StaticGameConfig()
		}
		return resolved
	}
	return gameConfig
}
