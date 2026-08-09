package rooms

import (
	"math"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestBotAStarStraightShortestPath(t *testing.T) {
	gameMap := botPathTestMap(7, 5, simulation.TileGround)
	start := gameMap.WorldPos(1, 2)
	goal := gameMap.WorldPos(5, 2)

	got, ok := nextBotPathDirection(gameMap, start, goal)
	if !ok {
		t.Fatal("nextBotPathDirection() failed for a straight passable path")
	}
	if got != (simulation.Vector2{X: 1}) {
		t.Fatalf("nextBotPathDirection() = %+v, want %+v", got, simulation.Vector2{X: 1})
	}
}

func TestBotAStarSymmetricObstacleUsesFHYX(t *testing.T) {
	gameMap := botPathTestMap(5, 5, simulation.TileGround)
	gameMap.Map[2][2] = simulation.TileWall
	start := gameMap.WorldPos(1, 2)
	goal := gameMap.WorldPos(3, 2)

	got, ok := nextBotPathDirection(gameMap, start, goal)
	if !ok {
		t.Fatal("nextBotPathDirection() failed around a symmetric obstacle")
	}
	// The upper and lower detours have equal F/H. The lower y row loses the
	// heap tie-break, so the first step is world +Y (row y-1).
	if got != (simulation.Vector2{Y: 1}) {
		t.Fatalf("nextBotPathDirection() = %+v, want upper-detour direction %+v", got, simulation.Vector2{Y: 1})
	}
}

func TestBotAStarWallAndWaterAreBlocked(t *testing.T) {
	for _, blocked := range []simulation.TileType{simulation.TileWall, simulation.TileWater} {
		t.Run(tileTypeName(blocked), func(t *testing.T) {
			gameMap := botPathTestMap(5, 5, simulation.TileGround)
			gameMap.Map[2][2] = blocked
			start := gameMap.WorldPos(1, 2)
			goal := gameMap.WorldPos(3, 2)

			got, ok := nextBotPathDirection(gameMap, start, goal)
			if !ok {
				t.Fatalf("nextBotPathDirection() failed around %s", tileTypeName(blocked))
			}
			if got != (simulation.Vector2{Y: 1}) {
				t.Fatalf("nextBotPathDirection() = %+v for %s, want %+v", got, tileTypeName(blocked), simulation.Vector2{Y: 1})
			}
		})
	}
}

func TestBotAStarGroundSpawnPointAndBushArePassable(t *testing.T) {
	for _, passable := range []simulation.TileType{
		simulation.TileGround,
		simulation.TileSpawnPoint,
		simulation.TileBush,
	} {
		t.Run(tileTypeName(passable), func(t *testing.T) {
			gameMap := botPathTestMap(3, 1, simulation.TileWall)
			gameMap.Map[0][0] = simulation.TileGround
			gameMap.Map[0][1] = passable
			gameMap.Map[0][2] = simulation.TileGround

			got, ok := nextBotPathDirection(gameMap, gameMap.WorldPos(0, 0), gameMap.WorldPos(2, 0))
			if !ok {
				t.Fatalf("nextBotPathDirection() failed through %s", tileTypeName(passable))
			}
			if got != (simulation.Vector2{X: 1}) {
				t.Fatalf("nextBotPathDirection() = %+v through %s, want %+v", got, tileTypeName(passable), simulation.Vector2{X: 1})
			}
		})
	}
}

func TestBotAStarDisconnectedGoal(t *testing.T) {
	gameMap := botPathTestMap(5, 5, simulation.TileGround)
	for y := 0; y < gameMap.Height; y++ {
		gameMap.Map[y][2] = simulation.TileWall
	}
	start := gameMap.WorldPos(1, 2)
	goal := gameMap.WorldPos(3, 2)

	if _, ok := nextBotPathDirection(gameMap, start, goal); ok {
		t.Fatal("nextBotPathDirection() succeeded across a disconnected wall")
	}
}

func TestBotAStarInvalidStartAndGoal(t *testing.T) {
	gameMap := botPathTestMap(5, 5, simulation.TileGround)
	validStart := gameMap.WorldPos(1, 2)
	validGoal := gameMap.WorldPos(3, 2)
	tests := []struct {
		name  string
		start simulation.Vector2
		goal  simulation.Vector2
	}{
		{name: "start outside map", start: simulation.Vector2{X: 3}, goal: validGoal},
		{name: "goal outside map", start: validStart, goal: simulation.Vector2{X: -3}},
		{name: "start blocked", start: gameMap.WorldPos(2, 2), goal: validGoal},
		{name: "goal water", start: validStart, goal: gameMap.WorldPos(2, 2)},
	}
	gameMap.Map[2][2] = simulation.TileWater
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := nextBotPathDirection(gameMap, tt.start, tt.goal); ok {
				t.Fatal("nextBotPathDirection() succeeded for an invalid start or goal")
			}
		})
	}
}

func TestBotAStarStartEqualsGoalUsesDirectDirection(t *testing.T) {
	gameMap := botPathTestMap(5, 5, simulation.TileGround)
	start := gameMap.WorldPos(2, 2)
	goal := simulation.Vector2{X: start.X + 0.3, Y: start.Y + 0.4}

	got, ok := nextBotPathDirection(gameMap, start, goal)
	if !ok {
		t.Fatal("nextBotPathDirection() failed when start and goal share a tile")
	}
	want := simulation.Vector2{X: 0.6, Y: 0.8}
	if got != want {
		t.Fatalf("nextBotPathDirection() = %+v, want %+v", got, want)
	}
}

func TestBotWorldToTileRejectsMalformedAndOutsidePositions(t *testing.T) {
	gameMap := botPathTestMap(5, 5, simulation.TileGround)
	valid := gameMap.WorldPos(2, 2)
	if got, ok := worldToBotTile(gameMap, valid); !ok || got != (botTile{x: 2, y: 2}) {
		t.Fatalf("worldToBotTile(valid) = %+v, %t, want (2,2), true", got, ok)
	}
	for name, position := range map[string]simulation.Vector2{
		"outside left": {X: -3},
		"outside top":  {Y: 3},
		"nan":          {X: math.NaN()},
		"infinity":     {X: math.Inf(1)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := worldToBotTile(gameMap, position); ok {
				t.Fatal("worldToBotTile() accepted an invalid world position")
			}
		})
	}

	malformed := gameMap
	malformed.Map = malformed.Map[:4]
	if _, ok := worldToBotTile(malformed, valid); ok {
		t.Fatal("worldToBotTile() accepted a map with a mismatched row count")
	}
}

func TestRetreatGoalUsesExactSixWorldDistance(t *testing.T) {
	gameMap := botPathTestMapWithTileSize(15, 5, 1.2, simulation.TileGround)
	player := simulation.PlayerData{
		Pos:    simulation.Vector2{X: 1.2, Y: 0},
		Radius: simulation.DefaultPlayerRadius,
	}
	target := simulation.Vector2{X: 2.4, Y: 0}

	got, ok := retreatGoal(gameMap, player, target, 6)
	if !ok {
		t.Fatal("retreatGoal() failed for an open map")
	}
	want := simulation.Vector2{X: -4.8, Y: 0}
	if !botVectorsNear(got, want) {
		t.Fatalf("retreatGoal() = %+v, want raw six-world-unit target %+v", got, want)
	}
}

func TestRetreatGoalBacksOffFromFarBlockedTile(t *testing.T) {
	gameMap := botPathTestMapWithTileSize(15, 5, 1.2, simulation.TileGround)
	gameMap.Map[2][3] = simulation.TileWall
	player := simulation.PlayerData{
		Pos:    simulation.Vector2{X: 1.2, Y: 0},
		Radius: simulation.DefaultPlayerRadius,
	}
	target := simulation.Vector2{X: 2.4, Y: 0}

	got, ok := retreatGoal(gameMap, player, target, 6)
	if !ok {
		t.Fatal("retreatGoal() failed to back off from the blocked raw tile")
	}
	want := simulation.Vector2{X: -3.6, Y: 0}
	if !botVectorsNear(got, want) {
		t.Fatalf("retreatGoal() = %+v, want nearest valid backoff tile center %+v", got, want)
	}
}

func TestRetreatGoalIncludesPlayerRadiusInMapValidation(t *testing.T) {
	gameMap := botPathTestMapWithTileSize(15, 5, 1.2, simulation.TileGround)
	gameMap.Map[2][3] = simulation.TileWall
	player := simulation.PlayerData{
		Pos:    simulation.Vector2{X: 1.2, Y: 0},
		Radius: 0.7,
	}
	target := simulation.Vector2{X: 2.4, Y: 0}

	got, ok := retreatGoal(gameMap, player, target, 6)
	if !ok {
		t.Fatal("retreatGoal() failed after skipping the radius-colliding backoff tile")
	}
	want := simulation.Vector2{X: -2.4, Y: 0}
	if !botVectorsNear(got, want) {
		t.Fatalf("retreatGoal() = %+v, want radius-safe backoff tile center %+v", got, want)
	}
}

func TestRetreatGoalDoesNotCrossBeyondSubTileRawTarget(t *testing.T) {
	gameMap := botPathTestMap(7, 5, simulation.TileGround)
	player := simulation.PlayerData{
		Pos:    simulation.Vector2{X: 0, Y: 0},
		Radius: 0,
	}
	target := simulation.Vector2{X: 0.1, Y: 0}

	if _, ok := retreatGoal(gameMap, player, target, 0.1); ok {
		t.Fatal("retreatGoal() crossed beyond a raw target that stayed in the current tile")
	}
}

func TestRetreatGoalDoesNotExceedRequestedDistanceForDiagonalRawTarget(t *testing.T) {
	gameMap := botPathTestMapWithTileSize(15, 15, 1.2, simulation.TileGround)
	player := simulation.PlayerData{
		Pos:    gameMap.WorldPos(7, 7),
		Radius: 0,
	}
	target := simulation.Vector2{X: player.Pos.X + 1, Y: player.Pos.Y + 1}
	const maxRetreatDistance = 6.0

	got, ok := retreatGoal(gameMap, player, target, maxRetreatDistance)
	if !ok {
		t.Fatal("retreatGoal() failed for an open map with a diagonal raw target")
	}
	deltaX := got.X - player.Pos.X
	deltaY := got.Y - player.Pos.Y
	if distanceSquared := deltaX*deltaX + deltaY*deltaY; distanceSquared > maxRetreatDistance*maxRetreatDistance+1e-12 {
		t.Fatalf("retreatGoal() = %+v, distance squared %v exceeds requested max %v", got, distanceSquared, maxRetreatDistance*maxRetreatDistance)
	}
	want := simulation.Vector2{X: -4.8, Y: -3.6}
	if !botVectorsNear(got, want) {
		t.Fatalf("retreatGoal() = %+v, want farthest in-cap diagonal backoff center %+v", got, want)
	}
}

func TestRetreatGoalOrdersAsymmetricCornerCellsByDistance(t *testing.T) {
	gameMap := botPathTestMap(8, 8, simulation.TileGround)
	for _, blocked := range []botTile{
		{x: 0, y: 5},
		{x: 1, y: 5},
		{x: 1, y: 4},
		{x: 2, y: 4},
	} {
		gameMap.Map[blocked.y][blocked.x] = simulation.TileWall
	}
	player := simulation.PlayerData{
		Pos:    simulation.Vector2{X: -0.25, Y: 0.5},
		Radius: 0,
	}
	target := simulation.Vector2{X: 1.25, Y: 1.5}

	got, ok := retreatGoal(gameMap, player, target, math.Sqrt(13))
	if !ok {
		t.Fatal("retreatGoal() failed at an asymmetric corner crossing")
	}
	want := simulation.Vector2{X: -1.5, Y: 0.5}
	if !botVectorsNear(got, want) {
		t.Fatalf("retreatGoal() = %+v, want farther corner side center %+v", got, want)
	}
}

func TestRetreatGoalFailsWhenFarToNearCandidatesAreBlocked(t *testing.T) {
	gameMap := botPathTestMapWithTileSize(7, 5, 1.2, simulation.TileGround)
	for x := 0; x < 3; x++ {
		gameMap.Map[2][x] = simulation.TileWall
	}
	player := simulation.PlayerData{
		Pos:    simulation.Vector2{X: 0, Y: 0},
		Radius: simulation.DefaultPlayerRadius,
	}
	target := simulation.Vector2{X: 1.2, Y: 0}

	if _, ok := retreatGoal(gameMap, player, target, 6); ok {
		t.Fatal("retreatGoal() returned a goal after every non-current candidate was blocked")
	}
}

func botPathTestMap(width, height int, tile simulation.TileType) simulation.MapData {
	return botPathTestMapWithTileSize(width, height, 1, tile)
}

func botPathTestMapWithTileSize(width, height int, tileSize float64, tile simulation.TileType) simulation.MapData {
	rows := make([][]simulation.TileType, height)
	for y := range rows {
		rows[y] = make([]simulation.TileType, width)
		for x := range rows[y] {
			rows[y][x] = tile
		}
	}
	return simulation.MapData{
		Width:      width,
		Height:     height,
		MaxPlayers: 6,
		TileSize:   tileSize,
		Map:        rows,
	}
}

func tileTypeName(tile simulation.TileType) string {
	switch tile {
	case simulation.TileWall:
		return "wall"
	case simulation.TileWater:
		return "water"
	case simulation.TileSpawnPoint:
		return "spawn point"
	case simulation.TileBush:
		return "bush"
	default:
		return "ground"
	}
}

func botVectorsNear(got, want simulation.Vector2) bool {
	return math.Abs(got.X-want.X) <= 1e-12 && math.Abs(got.Y-want.Y) <= 1e-12
}
