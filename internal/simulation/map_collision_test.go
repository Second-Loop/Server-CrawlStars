package simulation

import (
	"math"
	"testing"
)

func TestCircleCandidateRangeCoversEveryExhaustiveCircleHit(t *testing.T) {
	t.Parallel()

	for _, gameMap := range []MapData{
		propertyMapData(9, 7, 1),
		propertyMapData(11, 5, 1.7),
	} {
		positions := []Vector2{
			gameMap.WorldPos(0, 0),
			gameMap.WorldPos(gameMap.Width-1, gameMap.Height-1),
			gameMap.WorldPos(gameMap.Width/2, gameMap.Height/2),
			{
				X: gameMap.WorldPos(0, 0).X + gameMap.TileSize*0.5,
				Y: gameMap.WorldPos(0, 0).Y - gameMap.TileSize*0.5,
			},
		}
		radii := []float64{
			-1,
			0,
			math.Nextafter(0, math.Inf(1)),
			gameMap.TileSize * 0.5,
			math.Nextafter(gameMap.TileSize*0.5, math.Inf(1)),
			gameMap.TileSize * 2.25,
		}

		for positionIndex, position := range positions {
			for radiusIndex, radius := range radii {
				candidates := gameMap.circleCandidateRange(position, radius)
				normalizedRadius := math.Max(radius, 0)
				for y, row := range gameMap.Map {
					for x := range row {
						if !gameMap.circleIntersectsTile(position, normalizedRadius, x, y) {
							continue
						}
						if !candidates.contains(x, y) {
							t.Fatalf("position %d radius %d omitted hit tile (%d,%d): range=%+v", positionIndex, radiusIndex, x, y, candidates)
						}
					}
				}
			}
		}
	}
}

func TestSegmentCandidateRangeCoversEveryExhaustiveWallHit(t *testing.T) {
	t.Parallel()

	gameMap := propertyMapData(13, 9, 1.3)
	center := gameMap.WorldPos(gameMap.Width/2, gameMap.Height/2)
	epsilon := 1e-12
	lines := []struct {
		name  string
		start Vector2
		end   Vector2
	}{
		{name: "horizontal", start: Vector2{X: center.X - 5, Y: center.Y + epsilon}, end: Vector2{X: center.X + 5, Y: center.Y + epsilon}},
		{name: "vertical", start: Vector2{X: center.X - epsilon, Y: center.Y - 5}, end: Vector2{X: center.X - epsilon, Y: center.Y + 5}},
		{name: "diagonal corner minus", start: Vector2{X: center.X - 5, Y: center.Y - 5 - epsilon}, end: Vector2{X: center.X + 5, Y: center.Y + 5 - epsilon}},
		{name: "diagonal corner plus", start: Vector2{X: center.X - 5, Y: center.Y - 5 + epsilon}, end: Vector2{X: center.X + 5, Y: center.Y + 5 + epsilon}},
		{name: "zero length", start: center, end: center},
	}
	for _, line := range lines {
		t.Run(line.name, func(t *testing.T) {
			candidates := gameMap.segmentCandidateRange(line.start, line.end)
			for y, row := range gameMap.Map {
				for x, tile := range row {
					if tile != TileWall {
						continue
					}
					center := gameMap.WorldPos(x, y)
					half := gameMap.TileSize * 0.5
					tileMin := Vector2{X: center.X - half, Y: center.Y - half}
					tileMax := Vector2{X: center.X + half, Y: center.Y + half}
					if _, hit := segmentAABBHit(line.start, line.end, tileMin, tileMax); hit && !candidates.contains(x, y) {
						t.Fatalf("omitted hit tile (%d,%d): range=%+v", x, y, candidates)
					}
				}
			}
		})
	}
}

func TestCandidateRangesAreClampedAndNarrowForLocalQueries(t *testing.T) {
	t.Parallel()

	gameMap := propertyMapData(129, 129, 1.2)
	position := gameMap.WorldPos(64, 64)
	circleCandidates := gameMap.circleCandidateRange(position, DefaultPlayerRadius)
	if !circleCandidates.clampedTo(gameMap) {
		t.Fatalf("circle range is not clamped: %+v", circleCandidates)
	}
	if circleCandidates.width() > 9 || circleCandidates.height() > 9 {
		t.Fatalf("circle range is not local: %+v", circleCandidates)
	}

	segmentCandidates := gameMap.segmentCandidateRange(position, gameMap.WorldPos(70, 70))
	if !segmentCandidates.clampedTo(gameMap) {
		t.Fatalf("segment range is not clamped: %+v", segmentCandidates)
	}
	if segmentCandidates.width() > 15 || segmentCandidates.height() > 15 {
		t.Fatalf("segment range is not local: %+v", segmentCandidates)
	}

	large := gameMap.circleCandidateRange(position, gameMap.TileSize*float64(gameMap.Width))
	if large.width() != gameMap.Width || large.height() != gameMap.Height {
		t.Fatalf("large circle range should cover the clamped map: %+v", large)
	}
}

func TestOptimizedCircleCollisionMatchesExhaustiveAtEdgeAndCornerEpsilon(t *testing.T) {
	t.Parallel()

	gameMap := propertyMapData(9, 9, 1.2)
	gameMap.Map[4][4] = TileWall
	state := &State{gameMap: gameMap}
	center := gameMap.WorldPos(4, 4)
	half := gameMap.TileSize * 0.5
	radii := []float64{0, DefaultPlayerRadius, gameMap.TileSize * 2.25}
	for _, radius := range radii {
		for _, epsilon := range []float64{-1e-12, 0, 1e-12} {
			tileMinX := center.X - half
			tileMinY := center.Y - half
			edgePosition := Vector2{X: tileMinX - radius + epsilon, Y: center.Y}
			cornerOffset := radius / math.Sqrt2
			cornerPosition := Vector2{X: tileMinX - cornerOffset + epsilon, Y: tileMinY - cornerOffset + epsilon}
			for name, position := range map[string]Vector2{"edge": edgePosition, "corner": cornerPosition} {
				got := state.collidesWithMap(position, radius, tileBlocksPlayer)
				want := exhaustiveCollidesWithMap(gameMap, position, radius, tileBlocksPlayer)
				if got != want {
					t.Fatalf("%s radius=%v epsilon=%v position=%+v: got=%t want=%t", name, radius, epsilon, position, got, want)
				}
			}
		}
	}
}

func TestMapCollisionCandidateRangesAreEmptyForMaplessState(t *testing.T) {
	t.Parallel()

	gameMap := MapData{}
	if got := gameMap.circleCandidateRange(Vector2{}, DefaultPlayerRadius); got.valid {
		t.Fatalf("mapless circle candidate range = %+v, want invalid", got)
	}
	if got := gameMap.segmentCandidateRange(Vector2{}, Vector2{X: 1}); got.valid {
		t.Fatalf("mapless segment candidate range = %+v, want invalid", got)
	}
	state := &State{gameMap: gameMap}
	if state.collidesWithMap(Vector2{}, DefaultPlayerRadius, tileBlocksPlayer) {
		t.Fatal("mapless circle query collided")
	}
	if got := state.firstBlockingSegmentT(Vector2{}, Vector2{X: 1}); !math.IsInf(got, 1) {
		t.Fatalf("mapless segment blocking t = %v, want +Inf", got)
	}
}

func TestOptimizedCircleCollisionMatchesExhaustiveOracle(t *testing.T) {
	t.Parallel()

	random := uint64(0x9e3779b97f4a7c15)
	for mapIndex := 0; mapIndex < 12; mapIndex++ {
		width := 4 + int(nextPropertyValue(&random)%13)
		height := 4 + int(nextPropertyValue(&random)%11)
		tileSize := []float64{0.75, 1, 1.2, 1.7}[nextPropertyValue(&random)%4]
		gameMap := propertyMapData(width, height, tileSize)
		state := &State{gameMap: gameMap}
		for sample := 0; sample < 160; sample++ {
			position := propertyPosition(gameMap, &random)
			radius := []float64{-1, 0, math.Nextafter(0, math.Inf(1)), tileSize * 0.5, tileSize * 2.25, tileSize * float64(width)}[nextPropertyValue(&random)%6]
			for name, blocksTile := range map[string]func(TileType) bool{
				"player":     tileBlocksPlayer,
				"projectile": tileBlocksProjectile,
			} {
				got := state.collidesWithMap(position, radius, blocksTile)
				want := exhaustiveCollidesWithMap(gameMap, position, radius, blocksTile)
				if got != want {
					t.Fatalf("map %d sample %d %s collision mismatch at position=%+v radius=%v: got=%t want=%t", mapIndex, sample, name, position, radius, got, want)
				}
			}
		}
	}
}

func TestOptimizedMeleeWallCollisionMatchesExhaustiveOracle(t *testing.T) {
	t.Parallel()

	random := uint64(0x243f6a8885a308d3)
	for mapIndex := 0; mapIndex < 10; mapIndex++ {
		width := 4 + int(nextPropertyValue(&random)%12)
		height := 4 + int(nextPropertyValue(&random)%10)
		tileSize := []float64{0.8, 1, 1.2, 1.9}[nextPropertyValue(&random)%4]
		gameMap := propertyMapData(width, height, tileSize)
		state := &State{gameMap: gameMap}
		for sample := 0; sample < 160; sample++ {
			start := propertyPosition(gameMap, &random)
			end := propertyPosition(gameMap, &random)
			got := state.firstBlockingSegmentT(start, end)
			want := exhaustiveFirstBlockingSegmentT(gameMap, start, end)
			if !sameCollisionT(got, want) {
				t.Fatalf("map %d sample %d wall collision mismatch start=%+v end=%+v: got=%v want=%v", mapIndex, sample, start, end, got, want)
			}
		}
	}
}

func exhaustiveCollidesWithMap(gameMap MapData, position Vector2, radius float64, blocksTile func(TileType) bool) bool {
	if gameMap.Width == 0 || gameMap.Height == 0 {
		return false
	}
	if radius < 0 {
		radius = 0
	}
	halfTileSize := gameMap.TileSize * 0.5
	minX := gameMap.WorldPos(0, 0).X - halfTileSize
	maxX := gameMap.WorldPos(gameMap.Width-1, 0).X + halfTileSize
	minY := gameMap.WorldPos(0, gameMap.Height-1).Y - halfTileSize
	maxY := gameMap.WorldPos(0, 0).Y + halfTileSize
	if position.X-radius < minX || position.X+radius > maxX || position.Y-radius < minY || position.Y+radius > maxY {
		return true
	}
	for y, row := range gameMap.Map {
		for x, tile := range row {
			if blocksTile(tile) && gameMap.circleIntersectsTile(position, radius, x, y) {
				return true
			}
		}
	}
	return false
}

func exhaustiveFirstBlockingSegmentT(gameMap MapData, start, end Vector2) float64 {
	if gameMap.Width == 0 || gameMap.Height == 0 {
		return math.Inf(1)
	}
	halfTileSize := gameMap.TileSize * 0.5
	min := Vector2{
		X: gameMap.WorldPos(0, 0).X - halfTileSize,
		Y: gameMap.WorldPos(0, gameMap.Height-1).Y - halfTileSize,
	}
	max := Vector2{
		X: gameMap.WorldPos(gameMap.Width-1, 0).X + halfTileSize,
		Y: gameMap.WorldPos(0, 0).Y + halfTileSize,
	}
	if start.X < min.X || start.X > max.X || start.Y < min.Y || start.Y > max.Y {
		return 0
	}
	direction := Vector2{X: end.X - start.X, Y: end.Y - start.Y}
	blockingT := math.Inf(1)
	if direction.X > 0 {
		blockingT = math.Min(blockingT, (max.X-start.X)/direction.X)
	} else if direction.X < 0 {
		blockingT = math.Min(blockingT, (min.X-start.X)/direction.X)
	}
	if direction.Y > 0 {
		blockingT = math.Min(blockingT, (max.Y-start.Y)/direction.Y)
	} else if direction.Y < 0 {
		blockingT = math.Min(blockingT, (min.Y-start.Y)/direction.Y)
	}
	for y, row := range gameMap.Map {
		for x, tile := range row {
			if tile != TileWall {
				continue
			}
			center := gameMap.WorldPos(x, y)
			tileMin := Vector2{X: center.X - halfTileSize, Y: center.Y - halfTileSize}
			tileMax := Vector2{X: center.X + halfTileSize, Y: center.Y + halfTileSize}
			if t, hit := segmentAABBHit(start, end, tileMin, tileMax); hit && t < blockingT {
				blockingT = t
			}
		}
	}
	return blockingT
}

func propertyMapData(width, height int, tileSize float64) MapData {
	rows := make([][]TileType, height)
	for y := range rows {
		rows[y] = make([]TileType, width)
		for x := range rows[y] {
			switch (x*17 + y*31 + width + height) % 11 {
			case 0, 1:
				rows[y][x] = TileWall
			case 2:
				rows[y][x] = TileWater
			case 3:
				rows[y][x] = TileBush
			default:
				rows[y][x] = TileGround
			}
		}
	}
	return MapData{Width: width, Height: height, MaxPlayers: 2, TileSize: tileSize, Map: rows}
}

func propertyPosition(gameMap MapData, random *uint64) Vector2 {
	minX := gameMap.WorldPos(0, 0).X - gameMap.TileSize*0.5
	maxX := gameMap.WorldPos(gameMap.Width-1, 0).X + gameMap.TileSize*0.5
	minY := gameMap.WorldPos(0, gameMap.Height-1).Y - gameMap.TileSize*0.5
	maxY := gameMap.WorldPos(0, 0).Y + gameMap.TileSize*0.5
	spanX := maxX - minX + gameMap.TileSize*2
	spanY := maxY - minY + gameMap.TileSize*2
	return Vector2{
		X: minX - gameMap.TileSize + spanX*float64(nextPropertyValue(random)%10000)/10000,
		Y: minY - gameMap.TileSize + spanY*float64(nextPropertyValue(random)%10000)/10000,
	}
}

func nextPropertyValue(value *uint64) uint64 {
	*value = *value*6364136223846793005 + 1442695040888963407
	return *value
}

func sameCollisionT(got, want float64) bool {
	if math.IsInf(got, 1) || math.IsInf(want, 1) {
		return math.IsInf(got, 1) && math.IsInf(want, 1)
	}
	return math.Abs(got-want) <= 1e-12
}
