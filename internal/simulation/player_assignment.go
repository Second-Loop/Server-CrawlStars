package simulation

type PlayerAssignment struct {
	ID            PlayerID
	Team          Team
	Slot          int
	SpawnPosition Vector2
}

func PlayerAssignments(playerIDs []PlayerID, config GameConfig) []PlayerAssignment {
	if len(playerIDs) == 0 {
		return nil
	}

	config = resolveAssignmentGameConfig(config)
	gameMap := config.Map
	spawns := mapSpawnPoints(gameMap)
	assignments := make([]PlayerAssignment, 0, len(playerIDs))
	for index, playerID := range playerIDs {
		team, slot, ok := config.TeamForPlayerIndex(index)
		if !ok {
			team = TeamRed
			slot = index
		}
		assignments = append(assignments, PlayerAssignment{
			ID:            playerID,
			Team:          team,
			Slot:          slot,
			SpawnPosition: spawnPositionForIndex(gameMap, spawns, index),
		})
	}
	return assignments
}

func resolveAssignmentGameConfig(config GameConfig) GameConfig {
	if config.Version != GameConfigVersion {
		return StaticGameConfig()
	}
	gameMap := config.Map
	if gameMap.TileSize <= 0 {
		gameMap.TileSize = config.Tile.Size
	}
	if gameMap.TileSize <= 0 {
		gameMap.TileSize = TileSize
	}
	resolvedMap, err := ResolveMapData(gameMap)
	if err != nil {
		config.Map = StaticGameConfig().Map
		return config
	}
	config.Map = resolvedMap
	return config
}

func mapSpawnPoints(gameMap MapData) []Vector2 {
	spawns := make([]Vector2, 0)
	for y, row := range gameMap.Map {
		for x, tile := range row {
			if tile == TileSpawnPoint {
				spawns = append(spawns, gameMap.WorldPos(x, y))
			}
		}
	}
	return spawns
}

func uniqueSpawnCapacity(gameMap MapData) int {
	positions := make(map[spawnTile]struct{})
	for y, row := range gameMap.Map {
		for x, tile := range row {
			if tile == TileSpawnPoint {
				positions[spawnTile{X: x, Y: y}] = struct{}{}
			}
		}
	}
	for _, tile := range fallbackSpawnTiles(gameMap) {
		positions[tile] = struct{}{}
	}
	return len(positions)
}

func spawnPositionForIndex(gameMap MapData, spawns []Vector2, index int) Vector2 {
	if index >= 0 && index < len(spawns) {
		return spawns[index]
	}
	return fallbackSpawnPosition(gameMap, spawns, index-len(spawns))
}

func fallbackSpawnPosition(gameMap MapData, occupied []Vector2, index int) Vector2 {
	tiles := fallbackSpawnTiles(gameMap)
	if len(tiles) == 0 {
		return Vector2{}
	}
	if index < 0 {
		index = 0
	}
	available := make([]spawnTile, 0, len(tiles))
	for _, tile := range tiles {
		if containsVector(occupied, gameMap.WorldPos(tile.X, tile.Y)) {
			continue
		}
		available = append(available, tile)
	}
	if len(available) > 0 {
		tiles = available
	}
	tile := tiles[index%len(tiles)]
	return gameMap.WorldPos(tile.X, tile.Y)
}

func containsVector(vectors []Vector2, target Vector2) bool {
	for _, vector := range vectors {
		if vector == target {
			return true
		}
	}
	return false
}

type spawnTile struct {
	X int
	Y int
}

func fallbackSpawnTiles(gameMap MapData) []spawnTile {
	if gameMap.Width <= 0 || gameMap.Height <= 0 {
		return nil
	}

	minX, minY := 0, 0
	maxX, maxY := gameMap.Width-1, gameMap.Height-1
	if gameMap.Width > 2 {
		minX = 1
		maxX = gameMap.Width - 2
	}
	if gameMap.Height > 2 {
		minY = 1
		maxY = gameMap.Height - 2
	}

	candidates := []spawnTile{
		{X: minX, Y: minY},
		{X: maxX, Y: maxY},
		{X: maxX, Y: minY},
		{X: minX, Y: maxY},
		{X: (minX + maxX) / 2, Y: (minY + maxY) / 2},
	}
	tiles := make([]spawnTile, 0, len(candidates))
	seen := make(map[spawnTile]bool, len(candidates))
	appendUnique := func(tile spawnTile) {
		if seen[tile] {
			return
		}
		if tile.Y < 0 || tile.Y >= len(gameMap.Map) || tile.X < 0 || tile.X >= len(gameMap.Map[tile.Y]) {
			return
		}
		if tileBlocksPlayer(gameMap.Map[tile.Y][tile.X]) {
			return
		}
		seen[tile] = true
		tiles = append(tiles, tile)
	}
	for _, candidate := range candidates {
		appendUnique(candidate)
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			appendUnique(spawnTile{X: x, Y: y})
		}
	}
	return tiles
}
