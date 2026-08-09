package rooms

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

type botControllerState struct {
	exploreEpoch          uint64
	hasExploreDestination bool
	exploreDestination    simulation.Vector2
	cachedPathStart       botTile
	cachedPathGoal        botTile
	cachedNextDirection   simulation.Vector2
	hasCachedPath         bool
}

type botObservation struct {
	roomID          string
	gameMap         simulation.MapData
	gameConfig      simulation.GameConfig
	players         []simulation.PlayerData
	projectiles     []simulation.ProjectileData
	currentTick     simulation.Tick
	nextAttackTicks map[simulation.PlayerID]simulation.Tick
}

type botProjectileThreat struct {
	projectile        simulation.ProjectileData
	direction         simulation.Vector2
	forwardDistance   float64
	collisionDistance float64
	awayDirection     simulation.Vector2
	hasAway           bool
}

func botInputForObservation(
	bot simulation.PlayerData,
	observation botObservation,
	state *botControllerState,
) (simulation.InputCommand, bool) {
	if !bot.IsBot || bot.IsDead {
		return simulation.InputCommand{}, false
	}
	if state == nil {
		state = &botControllerState{}
	}

	target, hasTarget := botTargetForObservation(bot, observation)
	moveDirection := simulation.Vector2{}
	if dodgeDirection, threat := botDodgeDirection(bot, observation); threat {
		moveDirection = dodgeDirection
	} else if !hasTarget {
		moveDirection = botExploreDirection(bot, observation, state)
	} else if botShouldRetreat(bot, observation.gameConfig) {
		moveDirection = botRetreatDirection(bot, target, observation, state)
	} else {
		moveDirection = cachedBotPathDirectionOrZero(botObservationMap(observation), bot.Pos, target.Pos, state)
	}

	input := simulation.InputCommand{
		PlayerID:     bot.ID,
		ClientTick:   0,
		MoveDir:      moveDirection,
		PressedSkill: false,
	}
	if !hasTarget {
		return input, true
	}

	attackDirection := botUnitDirection(bot.Pos, target.Pos)
	input.AttackDir = attackDirection
	if botTargetInNormalAttackRange(bot, target, observation) && botAttackReady(bot.ID, observation) {
		input.PressedAttack = true
	}
	return input, true
}

func botTargetForObservation(bot simulation.PlayerData, observation botObservation) (simulation.PlayerData, bool) {
	detectionRange := observation.gameConfig.Bot.DetectionRangeWorld
	if !finiteBotFloat(detectionRange) || detectionRange <= 0 {
		return simulation.PlayerData{}, false
	}
	maxDistanceSquared := detectionRange * detectionRange
	var selected simulation.PlayerData
	selectedDistanceSquared := 0.0
	found := false
	for _, candidate := range observation.players {
		if !botCanTargetPlayer(bot, candidate, observation.gameConfig.SelectedMode) {
			continue
		}
		deltaX := candidate.Pos.X - bot.Pos.X
		deltaY := candidate.Pos.Y - bot.Pos.Y
		distanceSquared := deltaX*deltaX + deltaY*deltaY
		if !finiteBotFloat(distanceSquared) || distanceSquared > maxDistanceSquared {
			continue
		}
		if !found || distanceSquared < selectedDistanceSquared ||
			(distanceSquared == selectedDistanceSquared && candidate.ID < selected.ID) {
			selected = candidate
			selectedDistanceSquared = distanceSquared
			found = true
		}
	}
	return selected, found
}

func botCanTargetPlayer(bot, candidate simulation.PlayerData, mode simulation.GameModeConfig) bool {
	if mode.Rules.TeamBehavior == "" {
		return candidate.ID != bot.ID && !candidate.IsDead && candidate.Team != bot.Team
	}
	return simulation.CanPlayerDamage(bot, candidate, mode)
}

func botAttackReady(botID simulation.PlayerID, observation botObservation) bool {
	readyTick, hasReadyTick := observation.nextAttackTicks[botID]
	return !hasReadyTick || observation.currentTick >= readyTick
}

func botTargetInNormalAttackRange(bot, target simulation.PlayerData, observation botObservation) bool {
	playerType, ok := observation.gameConfig.PlayerType(bot.CharacterType)
	if !ok {
		playerType = observation.gameConfig.DefaultPlayerType()
	}
	tileSize := botObservationTileSize(observation)
	rangeWorld := playerType.NormalAttack.RangeTiles * tileSize
	if !finiteBotFloat(rangeWorld) || rangeWorld <= 0 {
		return false
	}
	deltaX := target.Pos.X - bot.Pos.X
	deltaY := target.Pos.Y - bot.Pos.Y
	distanceSquared := deltaX*deltaX + deltaY*deltaY
	return finiteBotFloat(distanceSquared) && distanceSquared <= rangeWorld*rangeWorld
}

func botShouldRetreat(bot simulation.PlayerData, gameConfig simulation.GameConfig) bool {
	playerType, ok := gameConfig.PlayerType(bot.CharacterType)
	if !ok {
		playerType = gameConfig.DefaultPlayerType()
	}
	if !finiteBotFloat(playerType.HP) || playerType.HP <= 0 || !finiteBotFloat(bot.HP) {
		return false
	}
	return bot.HP/playerType.HP <= gameConfig.Bot.RetreatHPRatio
}

func botObservationMap(observation botObservation) simulation.MapData {
	if observation.gameMap.Width > 0 && observation.gameMap.Height > 0 && len(observation.gameMap.Map) > 0 {
		return observation.gameMap
	}
	return observation.gameConfig.Map
}

func botObservationTileSize(observation botObservation) float64 {
	if finiteBotFloat(observation.gameConfig.Tile.Size) && observation.gameConfig.Tile.Size > 0 {
		return observation.gameConfig.Tile.Size
	}
	gameMap := botObservationMap(observation)
	if finiteBotFloat(gameMap.TileSize) && gameMap.TileSize > 0 {
		return gameMap.TileSize
	}
	return simulation.TileSize
}

func botExploreDirection(bot simulation.PlayerData, observation botObservation, state *botControllerState) simulation.Vector2 {
	gameMap := botObservationMap(observation)
	if !state.hasExploreDestination {
		if !botSelectExploreDestination(bot, observation.roomID, gameMap, state) {
			return simulation.Vector2{}
		}
	} else if botDistanceSquared(bot.Pos, state.exploreDestination) <= observation.gameConfig.Bot.ExploreArrivalDistanceWorld*observation.gameConfig.Bot.ExploreArrivalDistanceWorld {
		state.hasExploreDestination = false
		invalidateBotPathCache(state)
		if !botSelectExploreDestination(bot, observation.roomID, gameMap, state) {
			return simulation.Vector2{}
		}
	}

	direction, ok := cachedBotPathDirection(gameMap, bot.Pos, state.exploreDestination, state)
	if !ok {
		state.hasExploreDestination = false
		invalidateBotPathCache(state)
		return simulation.Vector2{}
	}
	return direction
}

func botSelectExploreDestination(
	bot simulation.PlayerData,
	roomID string,
	gameMap simulation.MapData,
	state *botControllerState,
) bool {
	geometry, ok := botMapGeometryFor(gameMap)
	if !ok {
		return false
	}
	currentTile, ok := worldToBotTile(gameMap, bot.Pos)
	if !ok {
		return false
	}
	candidates := make([]botTile, 0, gameMap.Width*gameMap.Height)
	for y, row := range gameMap.Map {
		for x := range row {
			if botTilePassable(gameMap, botTile{x: x, y: y}) {
				candidates = append(candidates, botTile{x: x, y: y})
			}
		}
	}
	nextEpoch := state.exploreEpoch + 1
	selected, ok := selectBotExploreTile(roomID, bot.ID, candidates, currentTile, nextEpoch)
	if !ok {
		return false
	}
	state.exploreEpoch = nextEpoch
	state.hasExploreDestination = true
	state.exploreDestination = botTileWorldCenter(geometry, selected)
	invalidateBotPathCache(state)
	return true
}

func selectBotExploreTile(
	roomID string,
	botID simulation.PlayerID,
	candidates []botTile,
	current botTile,
	epoch uint64,
) (botTile, bool) {
	if len(candidates) == 0 {
		return botTile{}, false
	}
	sortedCandidates := append([]botTile(nil), candidates...)
	sort.Slice(sortedCandidates, func(i, j int) bool {
		if sortedCandidates[i].y != sortedCandidates[j].y {
			return sortedCandidates[i].y < sortedCandidates[j].y
		}
		return sortedCandidates[i].x < sortedCandidates[j].x
	})
	if len(sortedCandidates) > 1 {
		withoutCurrent := sortedCandidates[:0]
		for _, candidate := range sortedCandidates {
			if candidate != current {
				withoutCurrent = append(withoutCurrent, candidate)
			}
		}
		sortedCandidates = withoutCurrent
		if len(sortedCandidates) == 0 {
			return botTile{}, false
		}
	}

	var seed bytes.Buffer
	_ = binary.Write(&seed, binary.BigEndian, uint32(len(roomID)))
	_, _ = seed.WriteString(roomID)
	_ = binary.Write(&seed, binary.BigEndian, uint32(len(botID)))
	_, _ = seed.WriteString(string(botID))
	_ = binary.Write(&seed, binary.BigEndian, epoch)
	sum := sha256.Sum256(seed.Bytes())
	index := binary.BigEndian.Uint64(sum[:8]) % uint64(len(sortedCandidates))
	return sortedCandidates[index], true
}

func botRetreatDirection(
	bot simulation.PlayerData,
	target simulation.PlayerData,
	observation botObservation,
	state *botControllerState,
) simulation.Vector2 {
	gameMap := botObservationMap(observation)
	goal, ok := retreatGoal(gameMap, bot, target.Pos, observation.gameConfig.Bot.RetreatDistanceWorld)
	if !ok {
		invalidateBotPathCache(state)
		return simulation.Vector2{}
	}
	return cachedBotPathDirectionOrZero(gameMap, bot.Pos, goal, state)
}

func cachedBotPathDirectionOrZero(
	gameMap simulation.MapData,
	startPosition simulation.Vector2,
	goalPosition simulation.Vector2,
	state *botControllerState,
) simulation.Vector2 {
	direction, ok := cachedBotPathDirection(gameMap, startPosition, goalPosition, state)
	if !ok {
		return simulation.Vector2{}
	}
	return direction
}

func cachedBotPathDirection(
	gameMap simulation.MapData,
	startPosition simulation.Vector2,
	goalPosition simulation.Vector2,
	state *botControllerState,
) (simulation.Vector2, bool) {
	if state == nil {
		return nextBotPathDirection(gameMap, startPosition, goalPosition)
	}
	start, startOK := worldToBotTile(gameMap, startPosition)
	goal, goalOK := worldToBotTile(gameMap, goalPosition)
	if !startOK || !goalOK {
		invalidateBotPathCache(state)
		return simulation.Vector2{}, false
	}
	if start != goal && state.hasCachedPath && state.cachedPathStart == start && state.cachedPathGoal == goal {
		return state.cachedNextDirection, true
	}
	if start == goal {
		invalidateBotPathCache(state)
		return nextBotPathDirection(gameMap, startPosition, goalPosition)
	}
	direction, ok := nextBotPathDirection(gameMap, startPosition, goalPosition)
	if !ok {
		invalidateBotPathCache(state)
		return simulation.Vector2{}, false
	}
	state.cachedPathStart = start
	state.cachedPathGoal = goal
	state.cachedNextDirection = direction
	state.hasCachedPath = true
	return direction, true
}

func invalidateBotPathCache(state *botControllerState) {
	if state == nil {
		return
	}
	state.cachedPathStart = botTile{}
	state.cachedPathGoal = botTile{}
	state.cachedNextDirection = simulation.Vector2{}
	state.hasCachedPath = false
}

func botDodgeDirection(bot simulation.PlayerData, observation botObservation) (simulation.Vector2, bool) {
	threats := botProjectileThreats(bot, observation)
	if len(threats) == 0 {
		return simulation.Vector2{}, false
	}

	var sum simulation.Vector2
	hasZeroAway := false
	for _, threat := range threats {
		if !threat.hasAway {
			hasZeroAway = true
			continue
		}
		sum.X += threat.awayDirection.X
		sum.Y += threat.awayDirection.Y
	}
	sumLength := math.Hypot(sum.X, sum.Y)
	if !hasZeroAway && sumLength > 1e-12 {
		return cleanBotDirection(simulation.Vector2{X: sum.X / sumLength, Y: sum.Y / sumLength}), true
	}

	selected := threats[0]
	for _, threat := range threats[1:] {
		if threat.collisionDistance < selected.collisionDistance ||
			(threat.collisionDistance == selected.collisionDistance && threat.projectile.ID < selected.projectile.ID) {
			selected = threat
		}
	}
	plusNinety := simulation.Vector2{X: -selected.direction.Y, Y: selected.direction.X}
	minusNinety := simulation.Vector2{X: selected.direction.Y, Y: -selected.direction.X}
	if botDodgeCandidateIsClear(bot, botObservationMap(observation), plusNinety, observation.gameConfig) {
		return plusNinety, true
	}
	if botDodgeCandidateIsClear(bot, botObservationMap(observation), minusNinety, observation.gameConfig) {
		return minusNinety, true
	}
	return simulation.Vector2{}, true
}

func botProjectileThreats(bot simulation.PlayerData, observation botObservation) []botProjectileThreat {
	playersByID := make(map[simulation.PlayerID]simulation.PlayerData, len(observation.players))
	for _, player := range observation.players {
		playersByID[player.ID] = player
	}

	lookAhead := observation.gameConfig.Bot.ProjectileLookAheadWorld
	margin := observation.gameConfig.Bot.DodgeMarginWorld
	if !finiteBotFloat(lookAhead) || lookAhead <= 0 || !finiteBotFloat(margin) || margin < 0 {
		return nil
	}
	botRadius := bot.Radius
	if !finiteBotFloat(botRadius) || botRadius < 0 {
		return nil
	}

	threats := make([]botProjectileThreat, 0, len(observation.projectiles))
	for _, projectile := range observation.projectiles {
		if projectile.IsDestroyed || projectile.OwnerID == bot.ID {
			continue
		}
		owner, ownerOK := playersByID[projectile.OwnerID]
		if !ownerOK || !botProjectileCanDamage(owner, bot, observation.gameConfig.SelectedMode) {
			continue
		}
		projectileRadius := projectile.Radius
		if !finiteBotFloat(projectileRadius) || projectileRadius < 0 {
			continue
		}
		directionLength := math.Hypot(projectile.Dir.X, projectile.Dir.Y)
		if !finiteBotFloat(directionLength) || directionLength == 0 {
			continue
		}
		direction := simulation.Vector2{X: projectile.Dir.X / directionLength, Y: projectile.Dir.Y / directionLength}
		relativeX := bot.Pos.X - projectile.Pos.X
		relativeY := bot.Pos.Y - projectile.Pos.Y
		forwardDistance := relativeX*direction.X + relativeY*direction.Y
		if !finiteBotFloat(forwardDistance) || forwardDistance <= 0 || forwardDistance > lookAhead {
			continue
		}
		nearestX := projectile.Pos.X + direction.X*forwardDistance
		nearestY := projectile.Pos.Y + direction.Y*forwardDistance
		awayX := bot.Pos.X - nearestX
		awayY := bot.Pos.Y - nearestY
		awayLength := math.Hypot(awayX, awayY)
		collisionRadius := botRadius + projectileRadius + margin
		if !finiteBotFloat(awayLength) || !finiteBotFloat(collisionRadius) || awayLength > collisionRadius {
			continue
		}
		collisionHalfChordSquared := collisionRadius*collisionRadius - awayLength*awayLength
		if collisionHalfChordSquared < 0 {
			collisionHalfChordSquared = 0
		}
		collisionDistance := forwardDistance - math.Sqrt(collisionHalfChordSquared)
		if collisionDistance < 0 {
			collisionDistance = 0
		}
		awayDirection := simulation.Vector2{}
		hasAway := awayLength > 1e-12
		if hasAway {
			awayDirection = cleanBotDirection(simulation.Vector2{X: awayX / awayLength, Y: awayY / awayLength})
		}
		threats = append(threats, botProjectileThreat{
			projectile:        projectile,
			direction:         direction,
			forwardDistance:   forwardDistance,
			collisionDistance: collisionDistance,
			awayDirection:     awayDirection,
			hasAway:           hasAway,
		})
	}
	sort.Slice(threats, func(i, j int) bool {
		if threats[i].projectile.ID != threats[j].projectile.ID {
			return threats[i].projectile.ID < threats[j].projectile.ID
		}
		if threats[i].projectile.OwnerID != threats[j].projectile.OwnerID {
			return threats[i].projectile.OwnerID < threats[j].projectile.OwnerID
		}
		if threats[i].projectile.Pos.X != threats[j].projectile.Pos.X {
			return threats[i].projectile.Pos.X < threats[j].projectile.Pos.X
		}
		return threats[i].projectile.Pos.Y < threats[j].projectile.Pos.Y
	})
	return threats
}

func botProjectileCanDamage(owner, target simulation.PlayerData, mode simulation.GameModeConfig) bool {
	if mode.Rules.TeamBehavior == "" {
		return owner.ID != target.ID && !target.IsDead && owner.Team != target.Team
	}
	return simulation.CanPlayerDamage(owner, target, mode)
}

func botDodgeCandidateIsClear(
	bot simulation.PlayerData,
	gameMap simulation.MapData,
	direction simulation.Vector2,
	gameConfig simulation.GameConfig,
) bool {
	geometry, ok := botMapGeometryFor(gameMap)
	if !ok || !finiteBotVector(direction) {
		return false
	}
	tickRate := gameConfig.TickRate
	if tickRate <= 0 {
		tickRate = simulation.TickRate
	}
	stepDistance := bot.Speed / float64(tickRate)
	if !finiteBotFloat(stepDistance) || stepDistance < 0 {
		return false
	}
	candidate := simulation.Vector2{
		X: bot.Pos.X + direction.X*stepDistance,
		Y: bot.Pos.Y + direction.Y*stepDistance,
	}
	radius := bot.Radius
	if !finiteBotFloat(radius) || radius < 0 {
		return false
	}
	return !botMapCollidesWithPlayer(geometry, gameMap, candidate, radius)
}

func botDistanceSquared(from, to simulation.Vector2) float64 {
	deltaX := to.X - from.X
	deltaY := to.Y - from.Y
	return deltaX*deltaX + deltaY*deltaY
}

func cleanBotDirection(direction simulation.Vector2) simulation.Vector2 {
	if math.Abs(direction.X) <= 1e-12 {
		direction.X = 0
	}
	if math.Abs(direction.Y) <= 1e-12 {
		direction.Y = 0
	}
	return direction
}
