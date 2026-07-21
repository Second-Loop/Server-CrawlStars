package simulation

import (
	"math"
	"sort"
)

type Tick uint64

const (
	TickRate                = 30
	TickDuration            = 1.0 / TickRate
	TileSize                = 1.2
	DefaultPlayerSpeed      = 2.0
	DefaultPlayerRadius     = 0.5
	DefaultPlayerHP         = 4000.0
	DefaultProjectileSpeed  = 13.0
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
	ClientTick    int64    `json:"ClientTick"`
	MoveDir       Vector2  `json:"MoveDir"`
	AttackDir     Vector2  `json:"AttackDir"`
	PressedAttack bool     `json:"PressedAttack"`
}

type PlayerData struct {
	ID                      PlayerID      `json:"Id"`
	Team                    Team          `json:"Team"`
	Slot                    int           `json:"Slot"`
	IsBot                   bool          `json:"IsBot"`
	CharacterType           CharacterType `json:"CharacterType"`
	Pos                     Vector2       `json:"Pos"`
	MoveDir                 Vector2       `json:"MoveDir"`
	AttackDir               Vector2       `json:"AttackDir"`
	Speed                   float64       `json:"Speed"`
	Radius                  float64       `json:"Radius"`
	HP                      float64       `json:"HP"`
	PressedAttack           bool          `json:"PressedAttack"`
	IsDead                  bool          `json:"IsDead"`
	LastProcessedClientTick int64         `json:"LastProcessedClientTick"`
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
	TileBush       TileType = 3
	TileWater      TileType = 4
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

type attackState struct {
	charges       int
	rechargeTicks int
}

type State struct {
	tick              Tick
	players           []PlayerData
	projectiles       []ProjectileData
	nextProjectileSeq uint64
	gameMap           MapData
	gameConfig        GameConfig
	attackStates      map[PlayerID]attackState
	burstStates       map[PlayerID]burstState
	projectileRuntime map[ProjectileID]projectileRuntime
}

func NewState(players []PlayerData) *State {
	return NewStateWithConfig(players, Config{})
}

func NewStateWithConfig(players []PlayerData, config Config) *State {
	gameConfig := resolveStateGameConfig(config)
	normalizedPlayers := normalizePlayersWithConfig(players, gameConfig)
	attackStates := make(map[PlayerID]attackState, len(normalizedPlayers))
	for _, player := range normalizedPlayers {
		playerType, ok := gameConfig.PlayerType(player.CharacterType)
		if !ok {
			continue
		}
		attackStates[player.ID] = attackState{charges: playerType.NormalAttack.MaxCharges}
	}
	return &State{
		players:           normalizedPlayers,
		gameMap:           gameConfig.Map,
		gameConfig:        gameConfig,
		attackStates:      attackStates,
		burstStates:       make(map[PlayerID]burstState),
		projectileRuntime: make(map[ProjectileID]projectileRuntime),
	}
}

func (s *State) Step(inputs []InputCommand) Snapshot {
	for i := range s.players {
		s.players[i].PressedAttack = false
	}
	s.rechargeAttackCharges()
	s.moveProjectiles()

	for _, input := range orderedInputsByPlayerID(inputs) {
		s.applyInput(input)
	}

	s.tick++

	return Snapshot{
		Tick:        s.tick,
		Players:     clonePlayers(s.players),
		Projectiles: cloneProjectiles(s.projectiles),
	}
}

func orderedInputsByPlayerID(inputs []InputCommand) []InputCommand {
	if len(inputs) < 2 {
		return inputs
	}
	ordered := append([]InputCommand(nil), inputs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].PlayerID < ordered[j].PlayerID
	})
	return ordered
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
	for i := range cloned {
		playerType, ok := config.PlayerType(cloned[i].CharacterType)
		if !ok {
			playerType = config.DefaultPlayerType()
		}
		if cloned[i].Speed <= 0 {
			cloned[i].Speed = playerType.Speed
		}
		if cloned[i].Radius <= 0 {
			cloned[i].Radius = playerType.Radius
		}
		if cloned[i].HP <= 0 {
			cloned[i].HP = playerType.HP
		}
	}
	return cloned
}

func (s *State) applyInput(input InputCommand) {
	for i := range s.players {
		if s.players[i].ID != input.PlayerID {
			continue
		}
		if s.players[i].IsDead || !isFinite(input.MoveDir) || !isFinite(input.AttackDir) {
			return
		}
		if input.ClientTick < 0 {
			return
		}
		if input.ClientTick > 0 && input.ClientTick <= s.players[i].LastProcessedClientTick {
			return
		}
		if input.ClientTick > 0 {
			s.players[i].LastProcessedClientTick = input.ClientTick
		}

		moveDir := clampDirection(input.MoveDir)
		attackDir := normalizeDirection(input.AttackDir)
		s.players[i].MoveDir = moveDir
		s.players[i].AttackDir = attackDir

		movement := Vector2{
			X: s.players[i].Speed * s.tickDuration() * moveDir.X,
			Y: s.players[i].Speed * s.tickDuration() * moveDir.Y,
		}

		nextX := Vector2{X: s.players[i].Pos.X + movement.X, Y: s.players[i].Pos.Y}
		if !s.collidesWithMap(nextX, s.players[i].Radius, tileBlocksPlayer) {
			s.players[i].Pos = nextX
		}

		nextY := Vector2{X: s.players[i].Pos.X, Y: s.players[i].Pos.Y + movement.Y}
		if !s.collidesWithMap(nextY, s.players[i].Radius, tileBlocksPlayer) {
			s.players[i].Pos = nextY
		}
		if input.PressedAttack && attackDir != (Vector2{}) && s.consumeAttackCharge(input.PlayerID) {
			s.players[i].PressedAttack = true
			if emission, ok := s.newProjectileEmission(s.players[i]); ok {
				s.emitProjectiles([]projectileEmission{emission})
			}
		}
		return
	}
}

func (s *State) rechargeAttackCharges() {
	for playerID, state := range s.attackStates {
		attack, ok := s.normalAttackConfig(playerID)
		if !ok {
			continue
		}
		if state.charges >= attack.MaxCharges {
			state.charges = attack.MaxCharges
			state.rechargeTicks = 0
			s.attackStates[playerID] = state
			continue
		}

		state.rechargeTicks++
		restored := state.rechargeTicks / attack.RechargeTicks
		if restored > 0 {
			state.charges += restored
			state.rechargeTicks %= attack.RechargeTicks
		}
		if state.charges >= attack.MaxCharges {
			state.charges = attack.MaxCharges
			state.rechargeTicks = 0
		}
		s.attackStates[playerID] = state
	}
}

func (s *State) consumeAttackCharge(playerID PlayerID) bool {
	state, ok := s.attackStates[playerID]
	if !ok || state.charges <= 0 {
		return false
	}
	state.charges--
	s.attackStates[playerID] = state
	return true
}

func (s *State) moveProjectiles() {
	for i := range s.projectiles {
		projectile := &s.projectiles[i]
		if projectile.IsDestroyed {
			continue
		}

		runtime, hasRuntime := s.projectileRuntime[projectile.ID]
		stepDistance := projectile.Speed * s.tickDuration()
		reachedRange := false
		if hasRuntime {
			remaining := runtime.maxDistance - runtime.moved
			if remaining <= stepDistance+1e-12 {
				stepDistance = math.Max(remaining, 0)
				reachedRange = true
			}
		}
		next := Vector2{
			X: projectile.Pos.X + projectile.Dir.X*stepDistance,
			Y: projectile.Pos.Y + projectile.Dir.Y*stepDistance,
		}
		projectile.Pos = next
		if hasRuntime {
			runtime.moved += stepDistance
		}
		if s.collidesWithMap(next, projectile.Radius, tileBlocksProjectile) {
			projectile.IsDestroyed = true
		}
		if !projectile.IsDestroyed {
			s.applyProjectileHit(projectile)
		}
		if !projectile.IsDestroyed && reachedRange {
			projectile.IsDestroyed = true
		}
		if projectile.IsDestroyed {
			delete(s.projectileRuntime, projectile.ID)
		} else if hasRuntime {
			s.projectileRuntime[projectile.ID] = runtime
		}
	}
}

func (s *State) applyProjectileHit(projectile *ProjectileData) {
	for i := range s.players {
		if !s.canProjectileHit(*projectile, s.players[i]) {
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

func (s *State) canProjectileHit(projectile ProjectileData, target PlayerData) bool {
	if target.ID == projectile.OwnerID || target.IsDead {
		return false
	}

	rules := s.gameConfig.SelectedMode.Rules
	switch rules.TeamBehavior {
	case TeamBehaviorFreeForAll:
		return true
	case TeamBehaviorTwoTeams:
		if rules.FriendlyFire {
			return true
		}
		ownerTeam, ok := s.playerTeam(projectile.OwnerID)
		return ok && ownerTeam != target.Team
	default:
		return false
	}
}

func (s *State) playerTeam(playerID PlayerID) (Team, bool) {
	for i := range s.players {
		if s.players[i].ID == playerID {
			return s.players[i].Team, true
		}
	}
	return "", false
}

func (s *State) tickDuration() float64 {
	if s.gameConfig.TickRate <= 0 {
		return TickDuration
	}
	return 1.0 / float64(s.gameConfig.TickRate)
}

func tileBlocksPlayer(tile TileType) bool {
	return tile == TileWall || tile == TileWater
}

func tileBlocksProjectile(tile TileType) bool {
	return tile == TileWall
}

func (s *State) collidesWithMap(position Vector2, radius float64, blocksTile func(TileType) bool) bool {
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
			if !blocksTile(tile) {
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

func clampDirection(direction Vector2) Vector2 {
	maxComponent := math.Max(math.Abs(direction.X), math.Abs(direction.Y))
	if maxComponent <= 1 && math.Hypot(direction.X, direction.Y) <= 1 {
		return direction
	}
	return normalizeDirection(direction)
}

func normalizeDirection(direction Vector2) Vector2 {
	maxComponent := math.Max(math.Abs(direction.X), math.Abs(direction.Y))
	if maxComponent == 0 {
		return direction
	}

	scaled := Vector2{X: direction.X / maxComponent, Y: direction.Y / maxComponent}
	scaledMagnitude := math.Hypot(scaled.X, scaled.Y)
	return Vector2{X: scaled.X / scaledMagnitude, Y: scaled.Y / scaledMagnitude}
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
	return dx*dx+dy*dy <= radius*radius+1e-12
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
