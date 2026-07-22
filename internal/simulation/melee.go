package simulation

import "math"

const (
	meleeContactEpsilon                  = 1e-9
	segmentDiscriminantRelativeTolerance = 16 * 2.220446049250313e-16
)

type meleeIntent struct {
	ownerIndex int
	origin     Vector2
	direction  Vector2
	attack     NormalAttackConfig
}

func (s *State) applyMeleeIntents(intents []meleeIntent) {
	if len(intents) == 0 {
		return
	}
	players := clonePlayers(s.players)
	damage := make([]float64, len(players))
	for _, intent := range intents {
		targetIndex := s.firstMeleeTarget(intent, players)
		if targetIndex >= 0 {
			damage[targetIndex] += intent.attack.DamagePerHit
		}
	}
	for index, amount := range damage {
		if amount == 0 {
			continue
		}
		s.players[index].HP -= amount
		if s.players[index].HP <= 0 {
			s.players[index].HP = 0
			s.players[index].IsDead = true
		}
	}
}

func (s *State) firstMeleeTarget(intent meleeIntent, players []PlayerData) int {
	if intent.ownerIndex < 0 || intent.ownerIndex >= len(players) {
		return -1
	}
	end := Vector2{
		X: intent.origin.X + intent.direction.X*intent.attack.RangeTiles*s.resolvedTileSize(),
		Y: intent.origin.Y + intent.direction.Y*intent.attack.RangeTiles*s.resolvedTileSize(),
	}
	blockingT := s.firstBlockingSegmentT(intent.origin, end)
	ownerID := players[intent.ownerIndex].ID
	for index, target := range players {
		if !s.canOwnerHit(ownerID, target) {
			continue
		}
		t, hit := segmentCircleHit(intent.origin, end, target.Pos, target.Radius)
		if hit && t < blockingT-meleeContactEpsilon {
			return index
		}
	}
	return -1
}

func (s *State) firstBlockingSegmentT(start, end Vector2) float64 {
	if s.gameMap.Width == 0 || s.gameMap.Height == 0 {
		return math.Inf(1)
	}
	tileSize := s.gameMap.TileSize
	halfTileSize := tileSize * 0.5
	min := Vector2{
		X: s.gameMap.WorldPos(0, 0).X - halfTileSize,
		Y: s.gameMap.WorldPos(0, s.gameMap.Height-1).Y - halfTileSize,
	}
	max := Vector2{
		X: s.gameMap.WorldPos(s.gameMap.Width-1, 0).X + halfTileSize,
		Y: s.gameMap.WorldPos(0, 0).Y + halfTileSize,
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

	for y, row := range s.gameMap.Map {
		for x, tile := range row {
			if tile != TileWall {
				continue
			}
			center := s.gameMap.WorldPos(x, y)
			tileMin := Vector2{X: center.X - halfTileSize, Y: center.Y - halfTileSize}
			tileMax := Vector2{X: center.X + halfTileSize, Y: center.Y + halfTileSize}
			if t, hit := segmentAABBHit(start, end, tileMin, tileMax); hit && t < blockingT {
				blockingT = t
			}
		}
	}
	return blockingT
}

func segmentCircleHit(start, end, center Vector2, radius float64) (float64, bool) {
	if radius < 0 {
		radius = 0
	}
	direction := Vector2{X: end.X - start.X, Y: end.Y - start.Y}
	offset := Vector2{X: start.X - center.X, Y: start.Y - center.Y}
	c := offset.X*offset.X + offset.Y*offset.Y - radius*radius
	if c <= 0 {
		return 0, true
	}
	a := direction.X*direction.X + direction.Y*direction.Y
	if a == 0 {
		return 0, false
	}
	b := 2 * (offset.X*direction.X + offset.Y*direction.Y)
	bSquared := b * b
	fourAC := 4 * a * c
	discriminant := bSquared - fourAC
	discriminantTolerance := segmentDiscriminantRelativeTolerance * (math.Abs(bSquared) + math.Abs(fourAC))
	if math.Abs(discriminant) <= discriminantTolerance {
		discriminant = 0
	} else if discriminant < 0 {
		return 0, false
	}
	t := (-b - math.Sqrt(discriminant)) / (2 * a)
	if t < -1e-12 || t > 1+1e-12 {
		return 0, false
	}
	return clamp(t, 0, 1), true
}

func segmentAABBHit(start, end, min, max Vector2) (float64, bool) {
	direction := Vector2{X: end.X - start.X, Y: end.Y - start.Y}
	enter, exit, ok := segmentSlab(start.X, direction.X, min.X, max.X, 0, 1)
	if !ok {
		return 0, false
	}
	enter, _, ok = segmentSlab(start.Y, direction.Y, min.Y, max.Y, enter, exit)
	if !ok {
		return 0, false
	}
	return enter, true
}

func segmentSlab(origin, direction, min, max, enter, exit float64) (float64, float64, bool) {
	if direction == 0 {
		return enter, exit, origin >= min && origin <= max
	}
	near := (min - origin) / direction
	far := (max - origin) / direction
	if near > far {
		near, far = far, near
	}
	if near > enter {
		enter = near
	}
	if far < exit {
		exit = far
	}
	return enter, exit, enter <= exit
}
