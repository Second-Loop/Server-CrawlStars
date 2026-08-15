package simulation

import (
	"math"
	"sort"
)

const teleportCollisionEpsilon = 1e-6

type teleportDistanceInterval struct {
	min float64
	max float64
}

func (s *State) applyLilySeedTeleport(ownerID PlayerID, targetIndex int, targetPosition Vector2, targetRadius float64, direction Vector2, behindDistance float64) {
	ownerIndex := -1
	for index := range s.players {
		if s.players[index].ID == ownerID {
			ownerIndex = index
			break
		}
	}
	if ownerIndex < 0 || s.players[ownerIndex].IsDead || targetIndex < 0 || targetIndex >= len(s.players) {
		return
	}

	ownerRadius := math.Max(s.players[ownerIndex].Radius, 0)
	minimumDistance := math.Max(targetRadius, 0) + ownerRadius + teleportCollisionEpsilon
	desiredDistance := math.Max(behindDistance, minimumDistance)
	distance, ok := s.largestValidTeleportDistance(
		targetPosition,
		direction,
		ownerRadius,
		minimumDistance,
		desiredDistance,
		ownerIndex,
		targetIndex,
	)
	if !ok {
		return
	}
	s.players[ownerIndex].Pos = addScaled(targetPosition, direction, distance)
}

func (s *State) largestValidTeleportDistance(origin, direction Vector2, radius, minimumDistance, desiredDistance float64, ownerIndex, targetIndex int) (float64, bool) {
	allowedMin := minimumDistance
	allowedMax := desiredDistance
	if s.gameMap.Width > 0 && s.gameMap.Height > 0 {
		mapMin, mapMax, ok := s.teleportBoundaryDistanceInterval(origin, direction, radius)
		if !ok {
			return 0, false
		}
		allowedMin = math.Max(allowedMin, mapMin)
		allowedMax = math.Min(allowedMax, mapMax)
		if allowedMax < allowedMin-1e-12 {
			return 0, false
		}
	}

	intervals := s.teleportBlockingIntervals(origin, direction, radius, allowedMin, allowedMax, ownerIndex, targetIndex)
	if len(intervals) == 0 {
		return allowedMax, true
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].min != intervals[j].min {
			return intervals[i].min < intervals[j].min
		}
		return intervals[i].max < intervals[j].max
	})
	merged := intervals[:0]
	for _, interval := range intervals {
		if interval.max < allowedMin || interval.min > allowedMax {
			continue
		}
		interval.min = math.Max(interval.min, allowedMin)
		interval.max = math.Min(interval.max, allowedMax)
		if len(merged) == 0 || interval.min > merged[len(merged)-1].max+1e-12 {
			merged = append(merged, interval)
			continue
		}
		if interval.max > merged[len(merged)-1].max {
			merged[len(merged)-1].max = interval.max
		}
	}

	candidate := allowedMax
	for index := len(merged) - 1; index >= 0; index-- {
		interval := merged[index]
		if candidate > interval.max+1e-12 {
			break
		}
		if candidate >= interval.min-1e-12 {
			candidate = math.Nextafter(interval.min, math.Inf(-1))
		}
	}
	for candidate >= allowedMin {
		position := addScaled(origin, direction, candidate)
		if !s.teleportDestinationBlocked(position, radius, ownerIndex, targetIndex) {
			return candidate, true
		}
		next := math.Nextafter(candidate, math.Inf(-1))
		if next >= candidate {
			break
		}
		candidate = next
	}
	return 0, false
}

func (s *State) teleportBoundaryDistanceInterval(origin, direction Vector2, radius float64) (float64, float64, bool) {
	tileSize := s.gameMap.TileSize
	if tileSize <= 0 {
		tileSize = s.resolvedTileSize()
	}
	halfTileSize := tileSize * 0.5
	minimum := Vector2{
		X: s.gameMap.WorldPos(0, 0).X - halfTileSize + radius,
		Y: s.gameMap.WorldPos(0, s.gameMap.Height-1).Y - halfTileSize + radius,
	}
	maximum := Vector2{
		X: s.gameMap.WorldPos(s.gameMap.Width-1, 0).X + halfTileSize - radius,
		Y: s.gameMap.WorldPos(0, 0).Y + halfTileSize - radius,
	}
	return rayAABBDistanceInterval(origin, direction, minimum, maximum)
}

func (s *State) teleportBlockingIntervals(origin, direction Vector2, radius, minimumDistance, desiredDistance float64, ownerIndex, targetIndex int) []teleportDistanceInterval {
	intervals := make([]teleportDistanceInterval, 0)
	for index, player := range s.players {
		if index == ownerIndex || index == targetIndex || player.IsDead {
			continue
		}
		radiusSum := radius + math.Max(player.Radius, 0)
		effectiveRadius := math.Sqrt(radiusSum*radiusSum + 1e-12)
		if interval, ok := rayCircleDistanceInterval(origin, direction, player.Pos, effectiveRadius); ok {
			intervals = append(intervals, interval)
		}
	}

	if s.gameMap.Width == 0 || s.gameMap.Height == 0 {
		return intervals
	}
	start := addScaled(origin, direction, minimumDistance)
	end := addScaled(origin, direction, desiredDistance)
	candidates := s.gameMap.candidateRangeForAABB(
		math.Min(start.X, end.X)-radius,
		math.Max(start.X, end.X)+radius,
		math.Min(start.Y, end.Y)-radius,
		math.Max(start.Y, end.Y)+radius,
	)
	if !candidates.valid {
		return intervals
	}
	for y := candidates.minY; y <= candidates.maxY; y++ {
		if y < 0 || y >= len(s.gameMap.Map) {
			continue
		}
		for x := candidates.minX; x <= candidates.maxX && x < len(s.gameMap.Map[y]); x++ {
			if x < 0 || !tileBlocksPlayer(s.gameMap.Map[y][x]) {
				continue
			}
			intervals = append(intervals, s.teleportTileBlockingIntervals(origin, direction, radius, x, y)...)
		}
	}
	return intervals
}

func (s *State) teleportDestinationBlocked(position Vector2, radius float64, ownerIndex, targetIndex int) bool {
	if s.collidesWithMap(position, radius, tileBlocksPlayer) {
		return true
	}
	for index, player := range s.players {
		if index == ownerIndex || index == targetIndex || player.IsDead {
			continue
		}
		if circlesOverlap(position, radius, player.Pos, player.Radius) {
			return true
		}
	}
	return false
}

func (s *State) teleportTileBlockingIntervals(origin, direction Vector2, radius float64, tileX, tileY int) []teleportDistanceInterval {
	center := s.gameMap.WorldPos(tileX, tileY)
	halfTileSize := s.gameMap.TileSize * 0.5
	minimum := Vector2{X: center.X - halfTileSize, Y: center.Y - halfTileSize}
	maximum := Vector2{X: center.X + halfTileSize, Y: center.Y + halfTileSize}
	intervals := make([]teleportDistanceInterval, 0, 6)
	for _, rectangle := range []struct {
		minimum Vector2
		maximum Vector2
	}{
		{
			minimum: Vector2{X: minimum.X - radius, Y: minimum.Y},
			maximum: Vector2{X: maximum.X + radius, Y: maximum.Y},
		},
		{
			minimum: Vector2{X: minimum.X, Y: minimum.Y - radius},
			maximum: Vector2{X: maximum.X, Y: maximum.Y + radius},
		},
	} {
		if minDistance, maxDistance, ok := rayAABBDistanceInterval(origin, direction, rectangle.minimum, rectangle.maximum); ok {
			intervals = append(intervals, teleportDistanceInterval{min: minDistance, max: maxDistance})
		}
	}
	for _, corner := range []Vector2{
		{X: minimum.X, Y: minimum.Y},
		{X: minimum.X, Y: maximum.Y},
		{X: maximum.X, Y: minimum.Y},
		{X: maximum.X, Y: maximum.Y},
	} {
		if interval, ok := rayCircleDistanceInterval(origin, direction, corner, radius); ok {
			intervals = append(intervals, interval)
		}
	}
	return intervals
}

func rayCircleDistanceInterval(origin, direction, center Vector2, radius float64) (teleportDistanceInterval, bool) {
	offset := Vector2{X: center.X - origin.X, Y: center.Y - origin.Y}
	projection := offset.X*direction.X + offset.Y*direction.Y
	perpendicularSquared := offset.X*offset.X + offset.Y*offset.Y - projection*projection
	radiusSquared := radius * radius
	if perpendicularSquared > radiusSquared+1e-12 {
		return teleportDistanceInterval{}, false
	}
	halfWidth := math.Sqrt(math.Max(radiusSquared-perpendicularSquared, 0))
	return teleportDistanceInterval{min: projection - halfWidth, max: projection + halfWidth}, true
}

func rayAABBDistanceInterval(origin, direction, minimum, maximum Vector2) (float64, float64, bool) {
	minDistance := math.Inf(-1)
	maxDistance := math.Inf(1)
	for _, axis := range []struct {
		origin    float64
		direction float64
		minimum   float64
		maximum   float64
	}{
		{origin.X, direction.X, minimum.X, maximum.X},
		{origin.Y, direction.Y, minimum.Y, maximum.Y},
	} {
		if math.Abs(axis.direction) <= 1e-15 {
			if axis.origin < axis.minimum || axis.origin > axis.maximum {
				return 0, 0, false
			}
			continue
		}
		first := (axis.minimum - axis.origin) / axis.direction
		second := (axis.maximum - axis.origin) / axis.direction
		if first > second {
			first, second = second, first
		}
		minDistance = math.Max(minDistance, first)
		maxDistance = math.Min(maxDistance, second)
		if maxDistance < minDistance {
			return 0, 0, false
		}
	}
	return minDistance, maxDistance, true
}
