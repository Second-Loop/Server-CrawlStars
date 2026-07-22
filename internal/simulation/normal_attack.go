package simulation

import (
	"math"
	"sort"
	"strconv"
)

type projectileRuntime struct {
	maxDistance float64
	moved       float64
}

type projectileEmission struct {
	ownerID        PlayerID
	direction      Vector2
	attack         NormalAttackConfig
	projectile     ProjectileTypeConfig
	projectileType ProjectileType
	ordinal        int
	snapshotTick   Tick
}

type attackIntent struct {
	playerIndex int
	owner       PlayerData
	direction   Vector2
	attack      NormalAttackConfig
}

type burstState struct {
	direction      Vector2
	attack         NormalAttackConfig
	activationTick Tick
	nextOrdinal    int
}

func (s *State) resolvedTileSize() float64 {
	if s.gameMap.TileSize > 0 {
		return s.gameMap.TileSize
	}
	if s.gameConfig.Tile.Size > 0 {
		return s.gameConfig.Tile.Size
	}
	return TileSize
}

func (s *State) normalAttackConfig(playerID PlayerID) (NormalAttackConfig, bool) {
	for _, player := range s.players {
		if player.ID != playerID {
			continue
		}
		playerType, ok := s.gameConfig.PlayerType(player.CharacterType)
		if !ok {
			return NormalAttackConfig{}, false
		}
		return playerType.NormalAttack, true
	}
	return NormalAttackConfig{}, false
}

func (s *State) newProjectileEmission(ownerID PlayerID, direction Vector2, attack NormalAttackConfig, ordinal int, snapshotTick Tick) (projectileEmission, bool) {
	if attack.Projectile == nil {
		return projectileEmission{}, false
	}
	projectileType, ok := s.gameConfig.ProjectileType(attack.Projectile.Type)
	if !ok {
		return projectileEmission{}, false
	}
	return projectileEmission{
		ownerID:        ownerID,
		direction:      direction,
		attack:         attack,
		projectile:     projectileType,
		projectileType: attack.Projectile.Type,
		ordinal:        ordinal,
		snapshotTick:   snapshotTick,
	}, true
}

func (s *State) approveProjectileAttack(intent attackIntent, snapshotTick Tick) []projectileEmission {
	if intent.attack.Kind == NormalAttackBurstProjectile {
		if _, active := s.burstStates[intent.owner.ID]; active {
			return nil
		}
	}
	if !s.consumeAttackCharge(intent.owner.ID) {
		return nil
	}
	s.players[intent.playerIndex].PressedAttack = true

	switch intent.attack.Kind {
	case NormalAttackSpreadProjectile:
		projectile := intent.attack.Projectile
		if projectile == nil {
			return nil
		}
		emissions := make([]projectileEmission, 0, len(projectile.DirectionOffsetsDegrees))
		for ordinal, offset := range projectile.DirectionOffsetsDegrees {
			direction := rotateDirection(intent.direction, offset)
			if emission, ok := s.newProjectileEmission(intent.owner.ID, direction, intent.attack, ordinal, snapshotTick); ok {
				emissions = append(emissions, emission)
			}
		}
		return emissions
	case NormalAttackBurstProjectile:
		s.burstStates[intent.owner.ID] = burstState{
			direction:      intent.direction,
			attack:         intent.attack,
			activationTick: snapshotTick,
			nextOrdinal:    1,
		}
		emission, ok := s.newProjectileEmission(intent.owner.ID, intent.direction, intent.attack, 0, snapshotTick)
		if !ok {
			return nil
		}
		return []projectileEmission{emission}
	default:
		return nil
	}
}

func (s *State) approveMeleeAttack(intent attackIntent) (meleeIntent, bool) {
	if intent.attack.Kind != NormalAttackMelee || !s.consumeAttackCharge(intent.owner.ID) {
		return meleeIntent{}, false
	}
	s.players[intent.playerIndex].PressedAttack = true
	return meleeIntent{
		ownerIndex: intent.playerIndex,
		origin:     intent.owner.Pos,
		direction:  intent.direction,
		attack:     intent.attack,
	}, true
}

func (s *State) collectDueBurstEmissions(snapshotTick Tick) []projectileEmission {
	ownerIDs := s.orderedBurstOwnerIDs()
	emissions := make([]projectileEmission, 0, len(ownerIDs))
	for _, ownerID := range ownerIDs {
		burst := s.burstStates[ownerID]
		owner, ok := s.playerByID(ownerID)
		if !ok || owner.IsDead {
			delete(s.burstStates, ownerID)
			continue
		}
		projectile := burst.attack.Projectile
		if projectile == nil || burst.nextOrdinal >= projectile.Count {
			continue
		}
		dueTick := burst.activationTick + Tick(burst.nextOrdinal*projectile.IntervalTicks)
		if snapshotTick != dueTick {
			continue
		}
		if emission, ok := s.newProjectileEmission(ownerID, burst.direction, burst.attack, burst.nextOrdinal, snapshotTick); ok {
			emissions = append(emissions, emission)
		}
		burst.nextOrdinal++
		s.burstStates[ownerID] = burst
	}
	return emissions
}

func (s *State) finishCompletedBursts() {
	for _, ownerID := range s.orderedBurstOwnerIDs() {
		burst := s.burstStates[ownerID]
		if burst.attack.Projectile != nil && burst.nextOrdinal >= burst.attack.Projectile.Count {
			delete(s.burstStates, ownerID)
		}
	}
}

func (s *State) orderedBurstOwnerIDs() []PlayerID {
	ownerIDs := make([]PlayerID, 0, len(s.burstStates))
	for ownerID := range s.burstStates {
		ownerIDs = append(ownerIDs, ownerID)
	}
	sort.Slice(ownerIDs, func(i, j int) bool {
		return ownerIDs[i] < ownerIDs[j]
	})
	return ownerIDs
}

func rotateDirection(direction Vector2, degrees float64) Vector2 {
	radians := math.Mod(degrees, 360) * math.Pi / 180
	cosine := math.Cos(radians)
	sine := math.Sin(radians)
	return Vector2{
		X: direction.X*cosine - direction.Y*sine,
		Y: direction.X*sine + direction.Y*cosine,
	}
}

func (s *State) emitProjectiles(emissions []projectileEmission) {
	ordered := append([]projectileEmission(nil), emissions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].ownerID != ordered[j].ownerID {
			return ordered[i].ownerID < ordered[j].ownerID
		}
		return ordered[i].ordinal < ordered[j].ordinal
	})
	for _, emission := range ordered {
		owner, ok := s.playerByID(emission.ownerID)
		if !ok {
			continue
		}
		s.nextProjectileSeq++
		projectileID := ProjectileID("projectile-" + strconv.FormatUint(uint64(emission.snapshotTick), 10) + "-" + string(emission.ownerID) + "-" + strconv.FormatUint(s.nextProjectileSeq, 10))
		s.projectiles = append(s.projectiles, ProjectileData{
			ID:      projectileID,
			OwnerID: emission.ownerID,
			Pos:     owner.Pos,
			Dir:     emission.direction,
			Speed:   emission.projectile.Speed,
			Damage:  emission.attack.DamagePerHit,
			Radius:  emission.projectile.Radius,
			Type:    emission.projectileType,
		})
		s.projectileRuntime[projectileID] = projectileRuntime{
			maxDistance: emission.attack.RangeTiles * s.resolvedTileSize(),
		}
	}
}

func (s *State) playerByID(playerID PlayerID) (PlayerData, bool) {
	for _, player := range s.players {
		if player.ID == playerID {
			return player, true
		}
	}
	return PlayerData{}, false
}
