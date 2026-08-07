package simulation

import "math"

type mapIndexRange struct {
	minX  int
	maxX  int
	minY  int
	maxY  int
	valid bool
}

func (r mapIndexRange) contains(x, y int) bool {
	return r.valid && x >= r.minX && x <= r.maxX && y >= r.minY && y <= r.maxY
}

func (r mapIndexRange) width() int {
	if !r.valid || r.maxX < r.minX {
		return 0
	}
	return r.maxX - r.minX + 1
}

func (r mapIndexRange) height() int {
	if !r.valid || r.maxY < r.minY {
		return 0
	}
	return r.maxY - r.minY + 1
}

func (r mapIndexRange) clampedTo(gameMap MapData) bool {
	return r.valid && r.minX >= 0 && r.maxX < gameMap.Width && r.minY >= 0 && r.maxY < gameMap.Height
}

func (m MapData) circleCandidateRange(position Vector2, radius float64) mapIndexRange {
	if radius < 0 {
		radius = 0
	}
	return m.candidateRangeForAABB(
		position.X-radius,
		position.X+radius,
		position.Y-radius,
		position.Y+radius,
	)
}

func (m MapData) segmentCandidateRange(start, end Vector2) mapIndexRange {
	return m.candidateRangeForAABB(
		math.Min(start.X, end.X),
		math.Max(start.X, end.X),
		math.Min(start.Y, end.Y),
		math.Max(start.Y, end.Y),
	)
}

func (m MapData) candidateRangeForAABB(minX, maxX, minY, maxY float64) mapIndexRange {
	if m.Width <= 0 || m.Height <= 0 {
		return mapIndexRange{}
	}
	if minX > maxX {
		minX, maxX = maxX, minX
	}
	if minY > maxY {
		minY, maxY = maxY, minY
	}
	if math.IsNaN(minX) || math.IsNaN(maxX) || math.IsNaN(minY) || math.IsNaN(maxY) ||
		math.IsInf(minX, 0) || math.IsInf(maxX, 0) || math.IsInf(minY, 0) || math.IsInf(maxY, 0) {
		return mapIndexRange{minX: 0, maxX: m.Width - 1, minY: 0, maxY: m.Height - 1, valid: true}
	}

	tileSize := m.TileSize
	if tileSize <= 0 {
		tileSize = TileSize
	}
	mapMinX := -tileSize * float64(m.Width) * 0.5
	mapMinY := -tileSize * float64(m.Height) * 0.5
	minIndexX := conservativeTileIndex(minX, mapMinX, tileSize) - 1
	maxIndexX := conservativeTileIndex(maxX, mapMinX, tileSize) + 1
	minBottomY := conservativeTileIndex(minY, mapMinY, tileSize) - 1
	maxBottomY := conservativeTileIndex(maxY, mapMinY, tileSize) + 1
	minIndexY := m.Height - 1 - maxBottomY
	maxIndexY := m.Height - 1 - minBottomY

	return mapIndexRange{
		minX:  clampMapIndex(minIndexX, m.Width),
		maxX:  clampMapIndex(maxIndexX, m.Width),
		minY:  clampMapIndex(minIndexY, m.Height),
		maxY:  clampMapIndex(maxIndexY, m.Height),
		valid: true,
	}
}

func conservativeTileIndex(value, mapMin, tileSize float64) int {
	return int(math.Floor((value - mapMin) / tileSize))
}

func clampMapIndex(index, size int) int {
	if index < 0 {
		return 0
	}
	if index >= size {
		return size - 1
	}
	return index
}
