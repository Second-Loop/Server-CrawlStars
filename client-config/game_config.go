package clientconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"

	_ "embed"
)

const Version = 3

type Config struct {
	Version              int
	TileSize             float64
	PlayerRadius         float64
	Characters           []CharacterConfig
	NormalAttackCoolDown int
	ProjectileRadius     float64
}

type CharacterConfig struct {
	Type                 int
	NormalAttackDistance float64
	SkillAttackDistance  float64
	SkillAttackCoolDown  int
	MaxBullets           int
}

type rawConfig struct {
	Version              *int                  `json:"version"`
	TileSize             *float64              `json:"tileSize"`
	PlayerRadius         *float64              `json:"playerRadius"`
	Characters           *[]rawCharacterConfig `json:"characters"`
	NormalAttackCoolDown *int                  `json:"normalAttackCoolDown"`
	ProjectileRadius     *float64              `json:"projectileRadius"`
}

type rawCharacterConfig struct {
	Type                 *int     `json:"type"`
	NormalAttackDistance *float64 `json:"normalAttackDistance"`
	SkillAttackDistance  *float64 `json:"skillAttackDistance"`
	SkillAttackCoolDown  *int     `json:"skillAttackCoolDown"`
	MaxBullets           *int     `json:"maxBullets"`
}

//go:embed game-config.json
var defaultGameConfig []byte

func Reader() io.Reader {
	return bytes.NewReader(defaultGameConfig)
}

func Parse(data []byte) (Config, error) {
	var wire rawConfig
	if err := json.Unmarshal(data, &wire); err != nil {
		return Config{}, fmt.Errorf("decode client game config: %w", err)
	}
	return wire.resolve()
}

func (wire rawConfig) resolve() (Config, error) {
	if wire.Version == nil {
		return Config{}, fmt.Errorf("version is required")
	}
	if *wire.Version != Version {
		return Config{}, fmt.Errorf("version must be %d, got %d", Version, *wire.Version)
	}
	if err := requireFinitePositive("tileSize", wire.TileSize); err != nil {
		return Config{}, err
	}
	if err := requireFinitePositive("playerRadius", wire.PlayerRadius); err != nil {
		return Config{}, err
	}
	if wire.NormalAttackCoolDown == nil || *wire.NormalAttackCoolDown <= 0 {
		return Config{}, fmt.Errorf("normalAttackCoolDown must be a positive integer")
	}
	if err := requireFinitePositive("projectileRadius", wire.ProjectileRadius); err != nil {
		return Config{}, err
	}
	if wire.Characters == nil {
		return Config{}, fmt.Errorf("characters is required")
	}
	if len(*wire.Characters) != 3 {
		return Config{}, fmt.Errorf("characters must contain exactly 3 entries, got %d", len(*wire.Characters))
	}

	characters := make([]CharacterConfig, 0, len(*wire.Characters))
	seenTypes := make(map[int]struct{}, len(*wire.Characters))
	for index, character := range *wire.Characters {
		resolved, err := character.resolve(index)
		if err != nil {
			return Config{}, err
		}
		if resolved.Type < 0 || resolved.Type > 2 {
			return Config{}, fmt.Errorf("characters[%d].type must be one of 0, 1, 2, got %d", index, resolved.Type)
		}
		if _, duplicate := seenTypes[resolved.Type]; duplicate {
			return Config{}, fmt.Errorf("characters[%d].type duplicates %d", index, resolved.Type)
		}
		seenTypes[resolved.Type] = struct{}{}
		characters = append(characters, resolved)
	}

	return Config{
		Version:              *wire.Version,
		TileSize:             *wire.TileSize,
		PlayerRadius:         *wire.PlayerRadius,
		Characters:           characters,
		NormalAttackCoolDown: *wire.NormalAttackCoolDown,
		ProjectileRadius:     *wire.ProjectileRadius,
	}, nil
}

func (wire rawCharacterConfig) resolve(index int) (CharacterConfig, error) {
	if wire.Type == nil {
		return CharacterConfig{}, fmt.Errorf("characters[%d].type is required", index)
	}
	if err := requireFinitePositive(
		fmt.Sprintf("characters[%d].normalAttackDistance", index),
		wire.NormalAttackDistance,
	); err != nil {
		return CharacterConfig{}, err
	}
	if err := requireFinitePositive(
		fmt.Sprintf("characters[%d].skillAttackDistance", index),
		wire.SkillAttackDistance,
	); err != nil {
		return CharacterConfig{}, err
	}
	if wire.SkillAttackCoolDown == nil || *wire.SkillAttackCoolDown <= 0 {
		return CharacterConfig{}, fmt.Errorf("characters[%d].skillAttackCoolDown must be a positive integer", index)
	}
	if wire.MaxBullets == nil || *wire.MaxBullets <= 0 {
		return CharacterConfig{}, fmt.Errorf("characters[%d].maxBullets must be a positive integer", index)
	}

	return CharacterConfig{
		Type:                 *wire.Type,
		NormalAttackDistance: *wire.NormalAttackDistance,
		SkillAttackDistance:  *wire.SkillAttackDistance,
		SkillAttackCoolDown:  *wire.SkillAttackCoolDown,
		MaxBullets:           *wire.MaxBullets,
	}, nil
}

func requireFinitePositive(name string, value *float64) error {
	if value == nil {
		return fmt.Errorf("%s is required", name)
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value <= 0 {
		return fmt.Errorf("%s must be finite and positive", name)
	}
	return nil
}
