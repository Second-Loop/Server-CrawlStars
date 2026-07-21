package simulation

import (
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

func (s *State) newProjectileEmission(owner PlayerData) (projectileEmission, bool) {
	playerType, ok := s.gameConfig.PlayerType(owner.CharacterType)
	if !ok || playerType.NormalAttack.Projectile == nil {
		return projectileEmission{}, false
	}
	projectileType, ok := s.gameConfig.ProjectileType(playerType.NormalAttack.Projectile.Type)
	if !ok {
		return projectileEmission{}, false
	}
	return projectileEmission{
		ownerID:        owner.ID,
		direction:      owner.AttackDir,
		attack:         playerType.NormalAttack,
		projectile:     projectileType,
		projectileType: playerType.NormalAttack.Projectile.Type,
		ordinal:        0,
		snapshotTick:   s.tick + 1,
	}, true
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
