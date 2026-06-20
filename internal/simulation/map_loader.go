package simulation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	_ "embed"
)

const DefaultMapFixturePath = "internal/simulation/fixtures/default-map.json"

//go:embed fixtures/default-map.json
var defaultMapFixture []byte

func LoadDefaultMapFixture() (MapData, error) {
	return LoadMapData(bytes.NewReader(defaultMapFixture))
}

func LoadMapData(reader io.Reader) (MapData, error) {
	var gameMap MapData
	if err := json.NewDecoder(reader).Decode(&gameMap); err != nil {
		return MapData{}, fmt.Errorf("decode map data: %w", err)
	}
	return ResolveMapData(gameMap)
}

func ResolveMapData(gameMap MapData) (MapData, error) {
	normalized := normalizeMap(gameMap)
	if err := validateMapData(normalized); err != nil {
		return MapData{}, err
	}
	return normalized, nil
}

func validateMapData(gameMap MapData) error {
	if gameMap.Width <= 0 || gameMap.Height <= 0 {
		return fmt.Errorf("map width and height must be positive")
	}
	if gameMap.MaxPlayers <= 0 {
		return fmt.Errorf("map maxPlayers must be positive")
	}
	if gameMap.Width < 4 || gameMap.Height < 4 {
		return fmt.Errorf("map must be at least 4x4 for current spawn positions")
	}
	if gameMap.TileSize <= 0 {
		return fmt.Errorf("map tileSize must be positive")
	}
	if len(gameMap.Map) != gameMap.Height {
		return fmt.Errorf("map row count %d does not match height %d", len(gameMap.Map), gameMap.Height)
	}
	for y, row := range gameMap.Map {
		if len(row) != gameMap.Width {
			return fmt.Errorf("map row %d width %d does not match width %d", y, len(row), gameMap.Width)
		}
		for x, tile := range row {
			switch tile {
			case TileGround, TileWall, TileSpawnPoint:
			default:
				return fmt.Errorf("map tile at (%d,%d) has invalid value %d", x, y, tile)
			}
		}
	}
	return nil
}
