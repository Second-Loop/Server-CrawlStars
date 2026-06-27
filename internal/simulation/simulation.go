package simulation

import (
	"math"
	"strconv"
)

type Tick uint64

const (
	TickRate                = 30
	TickDuration            = 1.0 / TickRate
	TileSize                = 1.2
	DefaultPlayerSpeed      = 2.0
	DefaultPlayerRadius     = 0.5
	DefaultPlayerHP         = 100.0
	DefaultProjectileSpeed  = 13.0
	DefaultProjectileDamage = 10.0
	DefaultProjectileRadius = 0.3
)

type PlayerID string

type ProjectileID string

type Team string

const (
	TeamRed  Team = "red"
	TeamBlue Team = "blue"
)

type Vector2 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type InputCommand struct {
	PlayerID      PlayerID `json:"PlayerId"`
	MoveDir       Vector2  `json:"MoveDir"`
	AttackDir     Vector2  `json:"AttackDir"`
	PressedAttack bool     `json:"PressedAttack"`
}

type PlayerData struct {
	ID            PlayerID `json:"Id"`
	Team          Team     `json:"Team"`
	Slot          int      `json:"Slot"`
	Pos           Vector2  `json:"Pos"`
	MoveDir       Vector2  `json:"MoveDir"`
	AttackDir     Vector2  `json:"AttackDir"`
	Speed         float64  `json:"Speed"`
	Radius        float64  `json:"Radius"`
	HP            float64  `json:"HP"`
	PressedAttack bool     `json:"PressedAttack"`
	IsDead        bool     `json:"IsDead"`
}

type ProjectileType string

type ProjectileData struct {
	ID          ProjectileID   `json:"Id"`
	OwnerID     PlayerID       `json:"OwnerId"`
	Pos         Vector2        `json:"Pos"`
	Dir         Vector2        `json:"Dir"`
	Speed       float64        `json:"Speed"`
	Damage      float64        `json:"Damage"`
	Radius      float64        `json:"Radius"`
	Type        ProjectileType `json:"Type"`
	IsDestroyed bool           `json:"IsDestroyed"`
}

type Snapshot struct {
	Tick        Tick             `json:"Tick"`
	Players     []PlayerData     `json:"Players"`
	Projectiles []ProjectileData `json:"Projectiles"`
}

type TileType uint8

const (
	TileGround     TileType = 0
	TileWall       TileType = 1
	TileSpawnPoint TileType = 2
)

type MapData struct {
	Width      int          `json:"width"`
	Height     int          `json:"height"`
	Index      int          `json:"index"`
	MaxPlayers int          `json:"maxPlayers"`
	TileSize   float64      `json:"tileSize"`
	Map        [][]TileType `json:"map"`
}

type Config struct {
	Map  MapData
	Game GameConfig
}

type State struct {
	tick              Tick
	players           []PlayerData
	projectiles       []ProjectileData
	nextProjectileSeq uint64
	gameMap           MapData
	gameConfig        GameConfig
}

func NewState(players []PlayerData) *State {
	return NewStateWithConfig(players, Config{})
}

func NewStateWithConfig(players []PlayerData, config Config) *State {
	gameConfig := resolveStateGameConfig(config)
	return &State{
		players:    normalizePlayersWithConfig(players, gameConfig),
		gameMap:    gameConfig.Map,
		gameConfig: gameConfig,
	}
}

func (s *State) Step(inputs []InputCommand) Snapshot {
	s.moveProjectiles()

	for _, input := range inputs {
		s.applyInput(input)
	}

	s.tick++

	return Snapshot{
		Tick:        s.tick,
		Players:     clonePlayers(s.players),
		Projectiles: cloneProjectiles(s.projectiles),
	}
}

func StaticMapFixture() MapData {
	return MapData{
		Width:      5,
		Height:     5,
		Index:      0,
		MaxPlayers: 6,
		TileSize:   TileSize,
		Map: [][]TileType{
			{TileWall, TileWall, TileWall, TileWall, TileWall},
			{TileWall, TileGround, TileGround, TileGround, TileWall},
			{TileWall, TileGround, TileWall, TileGround, TileWall},
			{TileWall, TileGround, TileGround, TileGround, TileWall},
			{TileWall, TileWall, TileWall, TileWall, TileWall},
		},
	}
}

func (m MapData) WorldPos(x int, y int) Vector2 {
	tileSize := m.TileSize
	if tileSize <= 0 {
		tileSize = TileSize
	}
	start := Vector2{
		X: -tileSize * 0.5 * float64(m.Width-1),
		Y: tileSize * 0.5 * float64(m.Height-1),
	}
	return Vector2{
		X: start.X + float64(x)*tileSize,
		Y: start.Y - float64(y)*tileSize,
	}
}

func clonePlayers(players []PlayerData) []PlayerData {
	if len(players) == 0 {
		return nil
	}

	cloned := make([]PlayerData, len(players))
	copy(cloned, players)
	return cloned
}

func cloneProjectiles(projectiles []ProjectileData) []ProjectileData {
	if len(projectiles) == 0 {
		return nil
	}

	cloned := make([]ProjectileData, len(projectiles))
	copy(cloned, projectiles)
	return cloned
}

func normalizePlayers(players []PlayerData) []PlayerData {
	return normalizePlayersWithConfig(players, StaticGameConfig())
}

func normalizePlayersWithConfig(players []PlayerData, config GameConfig) []PlayerData {
	cloned := clonePlayers(players)
	defaultPlayer := config.DefaultPlayerType()
	for i := range cloned {
		if cloned[i].Speed <= 0 {
			cloned[i].Speed = defaultPlayer.Speed
		}
		if cloned[i].Radius <= 0 {
			cloned[i].Radius = defaultPlayer.Radius
		}
		if cloned[i].HP <= 0 {
			cloned[i].HP = defaultPlayer.HP
		}
	}
	return cloned
}

func (s *State) applyInput(input InputCommand) {
	if !isFinite(input.MoveDir) || !isFinite(input.AttackDir) {
		return
	}

	for i := range s.players {
		if s.players[i].ID != input.PlayerID {
			continue
		}

		s.players[i].MoveDir = input.MoveDir
		s.players[i].AttackDir = input.AttackDir
		s.players[i].PressedAttack = input.PressedAttack

		movement := Vector2{
			X: s.players[i].Speed * s.tickDuration() * input.MoveDir.X,
			Y: s.players[i].Speed * s.tickDuration() * input.MoveDir.Y,
		}

		nextX := Vector2{X: s.players[i].Pos.X + movement.X, Y: s.players[i].Pos.Y}
		if !s.collidesWithWall(nextX, s.players[i].Radius) {
			s.players[i].Pos = nextX
		}

		nextY := Vector2{X: s.players[i].Pos.X, Y: s.players[i].Pos.Y + movement.Y}
		if !s.collidesWithWall(nextY, s.players[i].Radius) {
			s.players[i].Pos = nextY
		}
		if input.PressedAttack && input.AttackDir != (Vector2{}) {
			s.projectiles = append(s.projectiles, s.newProjectile(s.players[i]))
		}
		return
	}
}

func (s *State) moveProjectiles() {
	for i := range s.projectiles {
		if s.projectiles[i].IsDestroyed {
			continue
		}

		next := Vector2{
			X: s.projectiles[i].Pos.X + s.projectiles[i].Dir.X*s.projectiles[i].Speed*s.tickDuration(),
			Y: s.projectiles[i].Pos.Y + s.projectiles[i].Dir.Y*s.projectiles[i].Speed*s.tickDuration(),
		}
		s.projectiles[i].Pos = next
		if s.collidesWithWall(next, s.projectiles[i].Radius) {
			s.projectiles[i].IsDestroyed = true
		}
		if !s.projectiles[i].IsDestroyed {
			s.applyProjectileHit(&s.projectiles[i])
		}
	}
}

func (s *State) applyProjectileHit(projectile *ProjectileData) {
	for i := range s.players {
		if s.players[i].ID == projectile.OwnerID || s.players[i].IsDead {
			continue
		}
		if !circlesOverlap(projectile.Pos, projectile.Radius, s.players[i].Pos, s.players[i].Radius) {
			continue
		}

		s.players[i].HP -= projectile.Damage
		if s.players[i].HP <= 0 {
			s.players[i].HP = 0
			s.players[i].IsDead = true
		}
		projectile.IsDestroyed = true
		return
	}
}

func (s *State) newProjectile(owner PlayerData) ProjectileData {
	s.nextProjectileSeq++
	defaultProjectile := s.gameConfig.DefaultProjectileType()
	return ProjectileData{
		ID:      ProjectileID("projectile-" + strconv.FormatUint(uint64(s.tick+1), 10) + "-" + string(owner.ID) + "-" + strconv.FormatUint(s.nextProjectileSeq, 10)),
		OwnerID: owner.ID,
		Pos:     owner.Pos,
		Dir:     owner.AttackDir,
		Speed:   defaultProjectile.Speed,
		Damage:  defaultProjectile.Damage,
		Radius:  defaultProjectile.Radius,
	}
}

func (s *State) tickDuration() float64 {
	if s.gameConfig.TickRate <= 0 {
		return TickDuration
	}
	return 1.0 / float64(s.gameConfig.TickRate)
}

func (s *State) collidesWithWall(position Vector2, radius float64) bool {
	if s.gameMap.Width == 0 || s.gameMap.Height == 0 {
		return false
	}

	if radius < 0 {
		radius = 0
	}
	tileSize := s.gameMap.TileSize
	halfTileSize := tileSize * 0.5
	minX := s.gameMap.WorldPos(0, 0).X - halfTileSize
	maxX := s.gameMap.WorldPos(s.gameMap.Width-1, 0).X + halfTileSize
	minY := s.gameMap.WorldPos(0, s.gameMap.Height-1).Y - halfTileSize
	maxY := s.gameMap.WorldPos(0, 0).Y + halfTileSize
	if position.X-radius < minX || position.X+radius > maxX || position.Y-radius < minY || position.Y+radius > maxY {
		return true
	}

	for y, row := range s.gameMap.Map {
		for x, tile := range row {
			if tile != TileWall {
				continue
			}
			if s.gameMap.circleIntersectsTile(position, radius, x, y) {
				return true
			}
		}
	}

	return false
}

func (m MapData) circleIntersectsTile(position Vector2, radius float64, tileX int, tileY int) bool {
	center := m.WorldPos(tileX, tileY)
	halfTileSize := m.TileSize * 0.5
	minX := center.X - halfTileSize
	minY := center.Y - halfTileSize
	maxX := center.X + halfTileSize
	maxY := center.Y + halfTileSize

	nearestX := clamp(position.X, minX, maxX)
	nearestY := clamp(position.Y, minY, maxY)
	dx := position.X - nearestX
	dy := position.Y - nearestY
	return dx*dx+dy*dy <= radius*radius
}

func normalizeMap(gameMap MapData) MapData {
	if gameMap.TileSize <= 0 {
		gameMap.TileSize = TileSize
	}
	if gameMap.Height == 0 {
		gameMap.Height = len(gameMap.Map)
	}
	if gameMap.Width == 0 {
		for _, row := range gameMap.Map {
			if len(row) > gameMap.Width {
				gameMap.Width = len(row)
			}
		}
	}
	gameMap.Map = cloneTiles(gameMap.Map)
	return gameMap
}

func cloneTiles(tiles [][]TileType) [][]TileType {
	if len(tiles) == 0 {
		return nil
	}

	cloned := make([][]TileType, len(tiles))
	for i := range tiles {
		if len(tiles[i]) == 0 {
			continue
		}
		cloned[i] = make([]TileType, len(tiles[i]))
		copy(cloned[i], tiles[i])
	}
	return cloned
}

func isFinite(vector Vector2) bool {
	return !math.IsNaN(vector.X) && !math.IsNaN(vector.Y) && !math.IsInf(vector.X, 0) && !math.IsInf(vector.Y, 0)
}

func circlesOverlap(a Vector2, aRadius float64, b Vector2, bRadius float64) bool {
	if aRadius < 0 {
		aRadius = 0
	}
	if bRadius < 0 {
		bRadius = 0
	}
	dx := a.X - b.X
	dy := a.Y - b.Y
	radius := aRadius + bRadius
	return dx*dx+dy*dy <= radius*radius
}

func clamp(value float64, min float64, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
