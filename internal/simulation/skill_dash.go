package simulation

import (
	"math"
	"sort"
)

const (
	dashCollisionEpsilon     = 1e-6
	dashCollisionTimeEpsilon = 1e-12
)

type skillDashIntent struct {
	playerIndex int
	direction   Vector2
	distance    float64
}

type skillDashCandidate struct {
	playerIndex int
	playerID    PlayerID
	origin      Vector2
	target      Vector2
	position    Vector2
	direction   Vector2
	distance    float64
	radius      float64
	active      bool
}

type skillDashContact struct {
	time         float64
	participants []int
}

func (s *State) applySkillDashes(intents []skillDashIntent) {
	if len(intents) == 0 {
		return
	}
	sort.SliceStable(intents, func(i, j int) bool {
		return s.players[intents[i].playerIndex].ID < s.players[intents[j].playerIndex].ID
	})
	candidates := make([]skillDashCandidate, 0, len(intents))
	dashPlayers := make(map[int]struct{}, len(intents))
	for _, intent := range intents {
		if intent.playerIndex < 0 || intent.playerIndex >= len(s.players) || intent.distance <= 0 {
			continue
		}
		player := s.players[intent.playerIndex]
		if player.IsDead {
			continue
		}
		candidate := skillDashCandidate{
			playerIndex: intent.playerIndex,
			playerID:    player.ID,
			origin:      player.Pos,
			position:    player.Pos,
			direction:   intent.direction,
			distance:    intent.distance,
			radius:      math.Max(player.Radius, 0),
			active:      true,
		}
		candidate.target = addScaled(candidate.origin, candidate.direction, candidate.distance)
		dashPlayers[intent.playerIndex] = struct{}{}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return
	}

	currentTime := 0.0
	for currentTime < 1-dashCollisionTimeEpsilon {
		earliest := math.Inf(1)
		contacts := make([]skillDashContact, 0, len(candidates))
		record := func(contact skillDashContact) {
			if contact.time < currentTime-dashCollisionTimeEpsilon || contact.time > 1+dashCollisionTimeEpsilon {
				return
			}
			if contact.time < earliest-dashCollisionTimeEpsilon {
				earliest = contact.time
				contacts = contacts[:0]
			}
			if math.Abs(contact.time-earliest) <= dashCollisionTimeEpsilon {
				contacts = append(contacts, contact)
			}
		}

		for index := range candidates {
			candidate := &candidates[index]
			if !candidate.active {
				continue
			}
			start := dashPositionAt(*candidate, currentTime)
			if localTime, hit := s.firstSkillDashMapContact(start, candidate.target, candidate.radius); hit {
				record(skillDashContact{time: globalDashTime(currentTime, localTime), participants: []int{index}})
			}
			for playerIndex, player := range s.players {
				if player.IsDead || playerIndex == candidate.playerIndex {
					continue
				}
				if _, dashed := dashPlayers[playerIndex]; dashed {
					continue
				}
				if localTime, hit := movingCirclesContactTime(start, candidate.target, candidate.radius, player.Pos, player.Pos, player.Radius); hit {
					record(skillDashContact{time: globalDashTime(currentTime, localTime), participants: []int{index}})
				}
			}
			for blockerIndex := range candidates {
				blocker := &candidates[blockerIndex]
				if blocker.active || blocker.playerIndex == candidate.playerIndex {
					continue
				}
				if localTime, hit := movingCirclesContactTime(start, candidate.target, candidate.radius, blocker.position, blocker.position, blocker.radius); hit {
					record(skillDashContact{time: globalDashTime(currentTime, localTime), participants: []int{index}})
				}
			}
		}

		for left := range candidates {
			if !candidates[left].active {
				continue
			}
			for right := left + 1; right < len(candidates); right++ {
				if !candidates[right].active {
					continue
				}
				leftStart := dashPositionAt(candidates[left], currentTime)
				rightStart := dashPositionAt(candidates[right], currentTime)
				if localTime, hit := movingCirclesContactTime(
					leftStart, candidates[left].target, candidates[left].radius,
					rightStart, candidates[right].target, candidates[right].radius,
				); hit {
					record(skillDashContact{time: globalDashTime(currentTime, localTime), participants: []int{left, right}})
				}
			}
		}

		if math.IsInf(earliest, 1) {
			for index := range candidates {
				if candidates[index].active {
					candidates[index].position = candidates[index].target
					candidates[index].active = false
				}
			}
			break
		}

		stop := make([]bool, len(candidates))
		for _, contact := range contacts {
			if math.Abs(contact.time-earliest) > dashCollisionTimeEpsilon {
				continue
			}
			for _, participant := range contact.participants {
				stop[participant] = true
			}
		}
		for index := range candidates {
			if !candidates[index].active || !stop[index] {
				continue
			}
			travel := math.Max(candidates[index].distance*earliest-dashCollisionEpsilon, 0)
			candidates[index].position = addScaled(candidates[index].origin, candidates[index].direction, travel)
			candidates[index].active = false
		}
		currentTime = math.Max(currentTime, earliest)
	}

	for _, candidate := range candidates {
		if candidate.active {
			candidate.position = candidate.target
		}
		s.players[candidate.playerIndex].Pos = candidate.position
	}
}

func dashPositionAt(candidate skillDashCandidate, time float64) Vector2 {
	return addScaled(candidate.origin, candidate.direction, candidate.distance*clamp(time, 0, 1))
}

func addScaled(origin, direction Vector2, distance float64) Vector2 {
	return Vector2{X: origin.X + direction.X*distance, Y: origin.Y + direction.Y*distance}
}

func globalDashTime(currentTime, localTime float64) float64 {
	return currentTime + (1-currentTime)*clamp(localTime, 0, 1)
}

func movingCirclesContactTime(startA, endA Vector2, radiusA float64, startB, endB Vector2, radiusB float64) (float64, bool) {
	relativeStart := Vector2{X: startA.X - startB.X, Y: startA.Y - startB.Y}
	relativeEnd := Vector2{X: endA.X - endB.X, Y: endA.Y - endB.Y}
	relativeDelta := Vector2{X: relativeEnd.X - relativeStart.X, Y: relativeEnd.Y - relativeStart.Y}
	radius := math.Max(radiusA, 0) + math.Max(radiusB, 0)
	startDistanceSquared := relativeStart.X*relativeStart.X + relativeStart.Y*relativeStart.Y
	if startDistanceSquared <= radius*radius+dashCollisionTimeEpsilon {
		separationRate := relativeStart.X*relativeDelta.X + relativeStart.Y*relativeDelta.Y
		endDistanceSquared := relativeEnd.X*relativeEnd.X + relativeEnd.Y*relativeEnd.Y
		if separationRate > 0 && endDistanceSquared > startDistanceSquared+dashCollisionTimeEpsilon {
			return 0, false
		}
		return 0, true
	}
	return segmentCircleHit(relativeStart, relativeEnd, Vector2{}, radius)
}

func (s *State) firstSkillDashMapContact(start, end Vector2, radius float64) (float64, bool) {
	if s.gameMap.Width == 0 || s.gameMap.Height == 0 {
		return 0, false
	}
	earliest := math.Inf(1)
	record := func(time float64, hit bool) {
		if hit && time < earliest {
			earliest = time
		}
	}

	tileSize := s.gameMap.TileSize
	halfTileSize := tileSize * 0.5
	mapMinX := s.gameMap.WorldPos(0, 0).X - halfTileSize + radius
	mapMaxX := s.gameMap.WorldPos(s.gameMap.Width-1, 0).X + halfTileSize - radius
	mapMinY := s.gameMap.WorldPos(0, s.gameMap.Height-1).Y - halfTileSize + radius
	mapMaxY := s.gameMap.WorldPos(0, 0).Y + halfTileSize - radius
	delta := Vector2{X: end.X - start.X, Y: end.Y - start.Y}
	if delta.X > 0 && end.X >= mapMaxX {
		record((mapMaxX-start.X)/delta.X, true)
	} else if delta.X < 0 && end.X <= mapMinX {
		record((mapMinX-start.X)/delta.X, true)
	}
	if delta.Y > 0 && end.Y >= mapMaxY {
		record((mapMaxY-start.Y)/delta.Y, true)
	} else if delta.Y < 0 && end.Y <= mapMinY {
		record((mapMinY-start.Y)/delta.Y, true)
	}

	candidates := s.gameMap.candidateRangeForAABB(
		math.Min(start.X, end.X)-radius,
		math.Max(start.X, end.X)+radius,
		math.Min(start.Y, end.Y)-radius,
		math.Max(start.Y, end.Y)+radius,
	)
	if candidates.valid {
		for y := candidates.minY; y <= candidates.maxY; y++ {
			if y < 0 || y >= len(s.gameMap.Map) {
				continue
			}
			row := s.gameMap.Map[y]
			for x := candidates.minX; x <= candidates.maxX; x++ {
				if x < 0 || x >= len(row) || !tileBlocksPlayer(row[x]) {
					continue
				}
				center := s.gameMap.WorldPos(x, y)
				tileMin := Vector2{X: center.X - halfTileSize, Y: center.Y - halfTileSize}
				tileMax := Vector2{X: center.X + halfTileSize, Y: center.Y + halfTileSize}
				time, hit := sweptCircleAABBContactTime(start, end, radius, tileMin, tileMax)
				record(time, hit)
			}
		}
	}
	if math.IsInf(earliest, 1) || earliest < -dashCollisionTimeEpsilon || earliest > 1+dashCollisionTimeEpsilon {
		return 0, false
	}
	return clamp(earliest, 0, 1), true
}

func sweptCircleAABBContactTime(start, end Vector2, radius float64, min, max Vector2) (float64, bool) {
	delta := Vector2{X: end.X - start.X, Y: end.Y - start.Y}
	earliest := math.Inf(1)
	record := func(time float64, valid bool) {
		if valid && time >= -dashCollisionTimeEpsilon && time <= 1+dashCollisionTimeEpsilon && time < earliest {
			earliest = time
		}
	}
	if delta.X > 0 {
		time := (min.X - radius - start.X) / delta.X
		y := start.Y + delta.Y*time
		record(time, y >= min.Y-dashCollisionTimeEpsilon && y <= max.Y+dashCollisionTimeEpsilon)
	} else if delta.X < 0 {
		time := (max.X + radius - start.X) / delta.X
		y := start.Y + delta.Y*time
		record(time, y >= min.Y-dashCollisionTimeEpsilon && y <= max.Y+dashCollisionTimeEpsilon)
	}
	if delta.Y > 0 {
		time := (min.Y - radius - start.Y) / delta.Y
		x := start.X + delta.X*time
		record(time, x >= min.X-dashCollisionTimeEpsilon && x <= max.X+dashCollisionTimeEpsilon)
	} else if delta.Y < 0 {
		time := (max.Y + radius - start.Y) / delta.Y
		x := start.X + delta.X*time
		record(time, x >= min.X-dashCollisionTimeEpsilon && x <= max.X+dashCollisionTimeEpsilon)
	}

	corners := []struct {
		point     Vector2
		validZone func(Vector2) bool
	}{
		{Vector2{X: min.X, Y: min.Y}, func(point Vector2) bool { return point.X <= min.X && point.Y <= min.Y }},
		{Vector2{X: min.X, Y: max.Y}, func(point Vector2) bool { return point.X <= min.X && point.Y >= max.Y }},
		{Vector2{X: max.X, Y: min.Y}, func(point Vector2) bool { return point.X >= max.X && point.Y <= min.Y }},
		{Vector2{X: max.X, Y: max.Y}, func(point Vector2) bool { return point.X >= max.X && point.Y >= max.Y }},
	}
	for _, corner := range corners {
		time, hit := segmentCircleHit(start, end, corner.point, radius)
		if !hit {
			continue
		}
		point := addScaled(start, delta, time)
		if !corner.validZone(point) {
			continue
		}
		if time <= dashCollisionTimeEpsilon {
			offset := Vector2{X: start.X - corner.point.X, Y: start.Y - corner.point.Y}
			if offset.X*delta.X+offset.Y*delta.Y > 0 {
				continue
			}
		}
		record(time, true)
	}
	if math.IsInf(earliest, 1) {
		return 0, false
	}
	return clamp(earliest, 0, 1), true
}
