package rooms

import (
	"container/heap"
	"math"
	"sort"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

type botTile struct {
	x int
	y int
}

type botPathNode struct {
	tile botTile
	g    int
	h    int
	f    int
}

type botOpenHeap []botPathNode

func (h botOpenHeap) Len() int {
	return len(h)
}

func (h botOpenHeap) Less(i, j int) bool {
	if h[i].f != h[j].f {
		return h[i].f < h[j].f
	}
	if h[i].h != h[j].h {
		return h[i].h < h[j].h
	}
	if h[i].tile.y != h[j].tile.y {
		return h[i].tile.y < h[j].tile.y
	}
	return h[i].tile.x < h[j].tile.x
}

func (h botOpenHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *botOpenHeap) Push(value any) {
	*h = append(*h, value.(botPathNode))
}

func (h *botOpenHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

var botNeighborDeltas = [...]botTile{
	{x: 0, y: -1},
	{x: 0, y: 1},
	{x: -1, y: 0},
	{x: 1, y: 0},
}

type botMapGeometry struct {
	width    int
	height   int
	tileSize float64
	left     float64
	top      float64
	originX  float64
	originY  float64
}

type botRetreatCandidate struct {
	tile            botTile
	center          simulation.Vector2
	distanceSquared float64
}

const botTraversalTieEpsilon = 1e-12

func worldToBotTile(gameMap simulation.MapData, position simulation.Vector2) (botTile, bool) {
	geometry, ok := botMapGeometryFor(gameMap)
	if !ok || !finiteBotVector(position) {
		return botTile{}, false
	}

	gridX := (position.X - geometry.left) / geometry.tileSize
	gridY := (geometry.top - position.Y) / geometry.tileSize
	if gridX < 0 || gridX >= float64(geometry.width) ||
		gridY < 0 || gridY >= float64(geometry.height) {
		return botTile{}, false
	}
	return botTile{x: int(math.Floor(gridX)), y: int(math.Floor(gridY))}, true
}

func nextBotPathDirection(
	gameMap simulation.MapData,
	startPosition simulation.Vector2,
	goalPosition simulation.Vector2,
) (simulation.Vector2, bool) {
	if _, ok := botMapGeometryFor(gameMap); !ok {
		return simulation.Vector2{}, false
	}
	start, ok := worldToBotTile(gameMap, startPosition)
	if !ok || !botTilePassable(gameMap, start) {
		return simulation.Vector2{}, false
	}
	goal, ok := worldToBotTile(gameMap, goalPosition)
	if !ok || !botTilePassable(gameMap, goal) {
		return simulation.Vector2{}, false
	}
	if start == goal {
		return botUnitDirection(startPosition, goalPosition), true
	}

	open := botOpenHeap{{tile: start, g: 0, h: botManhattan(start, goal), f: botManhattan(start, goal)}}
	heap.Init(&open)
	gScore := map[botTile]int{start: 0}
	cameFrom := make(map[botTile]botTile)

	for open.Len() > 0 {
		current := heap.Pop(&open).(botPathNode)
		bestG, known := gScore[current.tile]
		if !known || current.g != bestG {
			continue
		}
		if current.tile == goal {
			return botFirstStepDirection(start, goal, cameFrom)
		}

		for _, delta := range botNeighborDeltas {
			neighbor := botTile{x: current.tile.x + delta.x, y: current.tile.y + delta.y}
			if !botTilePassable(gameMap, neighbor) {
				continue
			}
			tentativeG := current.g + 1
			previousG, seen := gScore[neighbor]
			if seen && tentativeG >= previousG {
				continue
			}
			gScore[neighbor] = tentativeG
			cameFrom[neighbor] = current.tile
			h := botManhattan(neighbor, goal)
			heap.Push(&open, botPathNode{tile: neighbor, g: tentativeG, h: h, f: tentativeG + h})
		}
	}

	return simulation.Vector2{}, false
}

func retreatGoal(
	gameMap simulation.MapData,
	player simulation.PlayerData,
	target simulation.Vector2,
	retreatDistance float64,
) (simulation.Vector2, bool) {
	geometry, ok := botMapGeometryFor(gameMap)
	if !ok || !finiteBotVector(player.Pos) || !finiteBotVector(target) ||
		!finiteBotFloat(retreatDistance) || retreatDistance <= 0 {
		return simulation.Vector2{}, false
	}
	startTile, ok := worldToBotTile(gameMap, player.Pos)
	if !ok {
		return simulation.Vector2{}, false
	}
	radius := player.Radius
	if math.IsNaN(radius) || math.IsInf(radius, 0) {
		return simulation.Vector2{}, false
	}
	if radius < 0 {
		radius = 0
	}

	targetDirection := botUnitDirection(player.Pos, target)
	rawTarget := simulation.Vector2{
		X: player.Pos.X - targetDirection.X*retreatDistance,
		Y: player.Pos.Y - targetDirection.Y*retreatDistance,
	}
	tiles := botSupercoverTiles(geometry, gameMap, player.Pos, rawTarget)
	candidates := make([]botRetreatCandidate, 0, len(tiles))
	for _, tile := range tiles {
		center := botTileWorldCenter(geometry, tile)
		deltaX := center.X - player.Pos.X
		deltaY := center.Y - player.Pos.Y
		candidates = append(candidates, botRetreatCandidate{
			tile:            tile,
			center:          center,
			distanceSquared: deltaX*deltaX + deltaY*deltaY,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		distanceDelta := candidates[i].distanceSquared - candidates[j].distanceSquared
		if math.Abs(distanceDelta) > 1e-12 {
			return distanceDelta > 0
		}
		if candidates[i].tile.y != candidates[j].tile.y {
			return candidates[i].tile.y < candidates[j].tile.y
		}
		return candidates[i].tile.x < candidates[j].tile.x
	})
	for _, candidate := range candidates {
		if candidate.tile == startTile || !botTilePassable(gameMap, candidate.tile) {
			continue
		}
		if botMapCollidesWithPlayer(geometry, gameMap, candidate.center, radius) {
			continue
		}
		return candidate.center, true
	}
	return simulation.Vector2{}, false
}

func botMapGeometryFor(gameMap simulation.MapData) (botMapGeometry, bool) {
	if gameMap.Width <= 0 || gameMap.Height <= 0 || len(gameMap.Map) != gameMap.Height {
		return botMapGeometry{}, false
	}
	tileSize := gameMap.TileSize
	if tileSize <= 0 {
		tileSize = simulation.TileSize
	}
	if !finiteBotFloat(tileSize) || tileSize <= 0 {
		return botMapGeometry{}, false
	}
	for _, row := range gameMap.Map {
		if len(row) != gameMap.Width {
			return botMapGeometry{}, false
		}
	}
	return botMapGeometry{
		width:    gameMap.Width,
		height:   gameMap.Height,
		tileSize: tileSize,
		left:     -tileSize * float64(gameMap.Width) * 0.5,
		top:      tileSize * float64(gameMap.Height) * 0.5,
		originX:  -tileSize * 0.5 * float64(gameMap.Width-1),
		originY:  tileSize * 0.5 * float64(gameMap.Height-1),
	}, true
}

func botTilePassable(gameMap simulation.MapData, tile botTile) bool {
	if tile.x < 0 || tile.x >= gameMap.Width || tile.y < 0 || tile.y >= gameMap.Height ||
		tile.y >= len(gameMap.Map) || tile.x >= len(gameMap.Map[tile.y]) {
		return false
	}
	switch gameMap.Map[tile.y][tile.x] {
	case simulation.TileGround, simulation.TileSpawnPoint, simulation.TileBush:
		return true
	default:
		return false
	}
}

func botManhattan(a, b botTile) int {
	return absBotInt(a.x-b.x) + absBotInt(a.y-b.y)
}

func botFirstStepDirection(start, goal botTile, cameFrom map[botTile]botTile) (simulation.Vector2, bool) {
	step := goal
	for {
		parent, ok := cameFrom[step]
		if !ok {
			return simulation.Vector2{}, false
		}
		if parent == start {
			return simulation.Vector2{
				X: float64(step.x - start.x),
				Y: float64(start.y - step.y),
			}, true
		}
		step = parent
	}
}

func botUnitDirection(from, to simulation.Vector2) simulation.Vector2 {
	deltaX := to.X - from.X
	deltaY := to.Y - from.Y
	length := math.Hypot(deltaX, deltaY)
	if length == 0 {
		return simulation.Vector2{X: 1}
	}
	return simulation.Vector2{X: deltaX / length, Y: deltaY / length}
}

func botSupercoverTiles(
	geometry botMapGeometry,
	gameMap simulation.MapData,
	start simulation.Vector2,
	end simulation.Vector2,
) []botTile {
	startTile, ok := worldToBotTile(gameMap, start)
	if !ok || !finiteBotVector(end) {
		return nil
	}
	startGridX := (start.X - geometry.left) / geometry.tileSize
	startGridY := (geometry.top - start.Y) / geometry.tileSize
	endGridX := (end.X - geometry.left) / geometry.tileSize
	endGridY := (geometry.top - end.Y) / geometry.tileSize
	if !finiteBotFloat(startGridX) || !finiteBotFloat(startGridY) ||
		!finiteBotFloat(endGridX) || !finiteBotFloat(endGridY) {
		return nil
	}
	endTile := botTile{x: int(math.Floor(endGridX)), y: int(math.Floor(endGridY))}

	tiles := make([]botTile, 0, 8)
	appendUnique := func(tile botTile) {
		if len(tiles) > 0 && tiles[len(tiles)-1] == tile {
			return
		}
		for _, existing := range tiles {
			if existing == tile {
				return
			}
		}
		tiles = append(tiles, tile)
	}
	appendUnique(startTile)
	if startTile == endTile {
		return tiles
	}

	deltaX := endGridX - startGridX
	deltaY := endGridY - startGridY
	stepX, stepY := 0, 0
	if deltaX > 0 {
		stepX = 1
	} else if deltaX < 0 {
		stepX = -1
	}
	if deltaY > 0 {
		stepY = 1
	} else if deltaY < 0 {
		stepY = -1
	}
	if stepX == 0 && stepY == 0 {
		return tiles
	}

	current := startTile
	maxSteps := absBotInt(int(math.Floor(endGridX))-current.x) +
		absBotInt(int(math.Floor(endGridY))-current.y) + 4
	if maxSteps < 4 {
		maxSteps = 4
	}
	tMaxX, tDeltaX := botGridTraversal(deltaX, startGridX, current.x, stepX)
	tMaxY, tDeltaY := botGridTraversal(deltaY, startGridY, current.y, stepY)
	for step := 0; step < maxSteps; step++ {
		if math.Min(tMaxX, tMaxY) > 1 {
			break
		}
		if stepX == 0 || (stepY != 0 && tMaxY < tMaxX-botTraversalTieEpsilon) {
			current.y += stepY
			tMaxY += tDeltaY
			appendUnique(current)
			if current == endTile {
				return tiles
			}
			continue
		}
		if stepY == 0 || tMaxX < tMaxY-botTraversalTieEpsilon {
			current.x += stepX
			tMaxX += tDeltaX
			appendUnique(current)
			if current == endTile {
				return tiles
			}
			continue
		}

		// The segment passes exactly through a grid corner. Both side cells
		// belong to the supercover, followed by the diagonal cell.
		appendUnique(botTile{x: current.x + stepX, y: current.y})
		appendUnique(botTile{x: current.x, y: current.y + stepY})
		current.x += stepX
		current.y += stepY
		tMaxX += tDeltaX
		tMaxY += tDeltaY
		appendUnique(current)
		if current == endTile {
			return tiles
		}
	}
	return tiles
}

func botGridTraversal(delta, coordinate float64, cell, step int) (float64, float64) {
	if step == 0 {
		return math.Inf(1), math.Inf(1)
	}
	var nextBoundary float64
	if step > 0 {
		nextBoundary = float64(cell + 1)
	} else {
		nextBoundary = float64(cell)
	}
	return (nextBoundary - coordinate) / delta, 1 / math.Abs(delta)
}

func botTileWorldCenter(geometry botMapGeometry, tile botTile) simulation.Vector2 {
	return simulation.Vector2{
		X: geometry.originX + float64(tile.x)*geometry.tileSize,
		Y: geometry.originY - float64(tile.y)*geometry.tileSize,
	}
}

func botMapCollidesWithPlayer(
	geometry botMapGeometry,
	gameMap simulation.MapData,
	position simulation.Vector2,
	radius float64,
) bool {
	if radius < 0 {
		radius = 0
	}
	mapRight := geometry.left + float64(geometry.width)*geometry.tileSize
	mapBottom := geometry.top - float64(geometry.height)*geometry.tileSize
	if position.X-radius < geometry.left || position.X+radius > mapRight ||
		position.Y-radius < mapBottom || position.Y+radius > geometry.top {
		return true
	}
	halfTileSize := geometry.tileSize * 0.5
	for y, row := range gameMap.Map {
		for x, tile := range row {
			if tile != simulation.TileWall && tile != simulation.TileWater {
				continue
			}
			center := botTileWorldCenter(geometry, botTile{x: x, y: y})
			minX := center.X - halfTileSize
			maxX := center.X + halfTileSize
			minY := center.Y - halfTileSize
			maxY := center.Y + halfTileSize
			nearestX := botClamp(position.X, minX, maxX)
			nearestY := botClamp(position.Y, minY, maxY)
			deltaX := position.X - nearestX
			deltaY := position.Y - nearestY
			if deltaX*deltaX+deltaY*deltaY <= radius*radius {
				return true
			}
		}
	}
	return false
}

func botClamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func finiteBotVector(value simulation.Vector2) bool {
	return finiteBotFloat(value.X) && finiteBotFloat(value.Y)
}

func finiteBotFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func absBotInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
