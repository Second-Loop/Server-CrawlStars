package rooms

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

type roomListResponse struct {
	Rooms []roomResponse `json:"rooms"`
}

type matchmakingJoinRequest struct {
	GameMode string `json:"gameMode"`
}

type roomResponse struct {
	ID             string           `json:"id"`
	GameMode       string           `json:"gameMode"`
	Status         RoomStatus       `json:"status"`
	Players        []playerResponse `json:"players"`
	MaxPlayers     int              `json:"maxPlayers"`
	Map            mapResponse      `json:"map"`
	LatestSnapshot snapshotSummary  `json:"latestSnapshot"`
}

type mapResponse struct {
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Index      int     `json:"index"`
	MaxPlayers int     `json:"maxPlayers"`
	TileSize   float64 `json:"tileSize"`
	Map        [][]int `json:"map"`
}

type playerResponse struct {
	ID    string `json:"id"`
	Team  string `json:"team"`
	Slot  int    `json:"slot"`
	IsBot bool   `json:"isBot"`
}

type playerSessionResponse struct {
	Player        playerResponse `json:"player"`
	SessionToken  string         `json:"sessionToken"`
	WebSocketPath string         `json:"webSocketPath"`
}

type matchmakingJoinResponse struct {
	GameMode      string         `json:"gameMode"`
	Room          roomResponse   `json:"room"`
	Player        playerResponse `json:"player"`
	SessionToken  string         `json:"sessionToken"`
	WebSocketPath string         `json:"webSocketPath"`
}

type clearRoomsResponse struct {
	Deleted int `json:"deleted"`
}

type snapshotSummary struct {
	Tick            uint64 `json:"tick"`
	PlayerCount     int    `json:"playerCount"`
	ProjectileCount int    `json:"projectileCount"`
}

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type inputMessage struct {
	ClientTick    int64              `json:"ClientTick"`
	MoveDir       simulation.Vector2 `json:"MoveDir"`
	AttackDir     simulation.Vector2 `json:"AttackDir"`
	PressedAttack bool               `json:"PressedAttack"`
}

func (m *inputMessage) UnmarshalJSON(data []byte) error {
	var wire struct {
		ClientTick    json.RawMessage    `json:"ClientTick"`
		MoveDir       simulation.Vector2 `json:"MoveDir"`
		AttackDir     simulation.Vector2 `json:"AttackDir"`
		PressedAttack bool               `json:"PressedAttack"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*m = inputMessage{
		MoveDir:       wire.MoveDir,
		AttackDir:     wire.AttackDir,
		PressedAttack: wire.PressedAttack,
	}
	if len(wire.ClientTick) == 0 {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(wire.ClientTick), []byte("null")) {
		return errors.New("ClientTick must be an integer")
	}
	return json.Unmarshal(wire.ClientTick, &m.ClientTick)
}

type inputDisposition uint8

const (
	inputIgnored inputDisposition = iota
	inputStored
	inputInvalid
)

type inputEnvelope struct {
	Type string `json:"Type"`
}

type roomSnapshotMessage struct {
	Type     string       `json:"Type"`
	Snapshot roomSnapshot `json:"Snapshot"`
}

type roomSnapshot struct {
	Status      MatchStatus                 `json:"status,omitempty"`
	Countdown   int                         `json:"countdown,omitempty"`
	Tick        simulation.Tick             `json:"Tick"`
	Players     []simulation.PlayerData     `json:"Players"`
	Projectiles []simulation.ProjectileData `json:"Projectiles"`
}

type readyEventMessage struct {
	Type    string             `json:"Type"`
	Map     mapResponse        `json:"Map"`
	Players []readyEventPlayer `json:"Players"`
}

type readyEventPlayer struct {
	ID            string             `json:"Id"`
	Team          string             `json:"Team"`
	Slot          int                `json:"Slot"`
	IsBot         bool               `json:"IsBot"`
	SpawnPosition simulation.Vector2 `json:"SpawnPosition"`
}

type gameEndMessage struct {
	Type     string `json:"Type"`
	PlayerID string `json:"PlayerId"`
	Result   string `json:"Result"`
}

type errorMessage struct {
	Type  string   `json:"Type"`
	Error apiError `json:"Error"`
}

// toResponse reads room-owned state; callers must hold r.mu unless the room is
// not yet visible in the Store registry.
func (r *room) toResponse(gameMap simulation.MapData) roomResponse {
	players := make([]playerResponse, len(r.Players))
	copy(players, r.Players)
	latestSnapshot := r.latestSnapshot
	if latestSnapshot == (snapshotSummary{}) {
		latestSnapshot.PlayerCount = len(players)
	}
	return roomResponse{
		ID:             r.ID,
		GameMode:       r.gameConfig.SelectedMode.ID,
		Status:         r.Status,
		Players:        players,
		MaxPlayers:     gameMap.MaxPlayers,
		Map:            mapResponseFromSimulation(gameMap),
		LatestSnapshot: latestSnapshot,
	}
}

func mapResponseFromSimulation(gameMap simulation.MapData) mapResponse {
	return mapResponse{
		Width:      gameMap.Width,
		Height:     gameMap.Height,
		Index:      gameMap.Index,
		MaxPlayers: gameMap.MaxPlayers,
		TileSize:   gameMap.TileSize,
		Map:        tileRowsResponse(gameMap.Map),
	}
}

func tileRowsResponse(rows [][]simulation.TileType) [][]int {
	if len(rows) == 0 {
		return nil
	}

	result := make([][]int, len(rows))
	for y, row := range rows {
		result[y] = make([]int, len(row))
		for x, tile := range row {
			result[y][x] = int(tile)
		}
	}
	return result
}

func snapshotSummaryFromSnapshot(snapshot simulation.Snapshot) snapshotSummary {
	return snapshotSummary{
		Tick:            uint64(snapshot.Tick),
		PlayerCount:     len(snapshot.Players),
		ProjectileCount: len(snapshot.Projectiles),
	}
}

func webSocketPath(roomID string, playerID string, sessionToken string) string {
	return "/rooms/" + roomID + "/players/" + playerID + "?token=" + sessionToken
}

// The room delivery helpers below require r.mu so client membership and
// payload state are captured from one room-consistent point in time.
func (r *room) matchSnapshotDeliveries(status MatchStatus, countdown int) []webSocketDelivery {
	message := r.matchSnapshotMessage(status, countdown)
	deliveries := make([]webSocketDelivery, 0, len(r.clients))
	for _, session := range r.clients {
		if session != nil {
			deliveries = append(deliveries, webSocketDelivery{
				session: session,
				message: message,
			})
		}
	}
	return deliveries
}

func (r *room) matchSnapshotMessage(status MatchStatus, countdown int) roomSnapshotMessage {
	return roomSnapshotMessage{
		Type: "snapshot",
		Snapshot: roomSnapshot{
			Status:    status,
			Countdown: countdown,
			Tick:      0,
		},
	}
}

func (r *room) readyEventDeliveries() []webSocketDelivery {
	message := readyEventMessage{
		Type:    "Ready",
		Map:     mapResponseFromSimulation(r.gameConfig.Map),
		Players: readyEventPlayers(r.Players, r.gameConfig),
	}
	deliveries := make([]webSocketDelivery, 0, len(r.clients))
	for _, session := range r.clients {
		if session != nil {
			deliveries = append(deliveries, webSocketDelivery{
				session: session,
				message: message,
			})
		}
	}
	return deliveries
}

func roomSnapshotFromSimulation(snapshot simulation.Snapshot, status MatchStatus) roomSnapshot {
	return roomSnapshot{
		Status:      status,
		Tick:        snapshot.Tick,
		Players:     snapshot.Players,
		Projectiles: snapshot.Projectiles,
	}
}

func readyEventPlayers(players []playerResponse, gameConfig simulation.GameConfig) []readyEventPlayer {
	spawnedPlayers := simulationPlayers(players, gameConfig)
	result := make([]readyEventPlayer, 0, len(spawnedPlayers))
	for _, player := range spawnedPlayers {
		result = append(result, readyEventPlayer{
			ID:            string(player.ID),
			Team:          string(player.Team),
			Slot:          player.Slot,
			IsBot:         player.IsBot,
			SpawnPosition: player.Pos,
		})
	}
	return result
}

func (r *room) gameEndDeliveries(results map[string]gameEndResult) []webSocketDelivery {
	if len(results) == 0 {
		return nil
	}

	deliveries := make([]webSocketDelivery, 0, len(results))
	for _, player := range r.Players {
		result, ok := results[player.ID]
		if !ok {
			continue
		}
		session := r.clients[player.ID]
		if session == nil {
			continue
		}
		if r.finalizedGameEndSessions == nil {
			r.finalizedGameEndSessions = make(map[string]*clientSession)
		}
		r.finalizedGameEndSessions[player.ID] = session
		deliveries = append(deliveries, webSocketDelivery{
			session: session,
			message: gameEndMessage{
				Type:     "GameEnd",
				PlayerID: player.ID,
				Result:   result.String(),
			},
		})
	}
	return deliveries
}

// snapshotSessionsWithoutFinalizedGameEnd requires r.mu. A finalized player
// receives the result snapshot through its terminal handoff instead of the
// replaceable gameplay snapshot queue.
func (r *room) snapshotSessionsWithoutFinalizedGameEnd() []*clientSession {
	sessions := make([]*clientSession, 0, len(r.clients))
	for playerID, session := range r.clients {
		if session != nil && !r.hasFinalizedGameEndResult(playerID) {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

// clientSessions requires r.mu and captures the terminal close barrier before
// ending prevents any new attachment. Finalized sessions and any session whose
// transport close is still owned by the room stay in this barrier after
// releaseClient removes them from the current-client map.
func (r *room) clientSessions() []*clientSession {
	current := make([]*clientSession, 0, len(r.clients))
	for _, session := range r.clients {
		if session != nil {
			current = append(current, session)
		}
	}
	finalized := make([]*clientSession, 0, len(r.finalizedGameEndSessions))
	for _, session := range r.finalizedGameEndSessions {
		if session != nil {
			finalized = append(finalized, session)
		}
	}
	closing := make([]*clientSession, 0, len(r.closeBarrierSessions))
	for session := range r.closeBarrierSessions {
		if session != nil {
			closing = append(closing, session)
		}
	}
	return uniqueClientSessions(current, finalized, closing)
}

func simulationPlayers(players []playerResponse, gameConfig simulation.GameConfig) []simulation.PlayerData {
	playerIDs := make([]simulation.PlayerID, 0, len(players))
	for _, player := range players {
		playerIDs = append(playerIDs, simulation.PlayerID(player.ID))
	}
	assignments := simulation.PlayerAssignments(playerIDs, gameConfig)
	result := make([]simulation.PlayerData, 0, len(players))
	for index, player := range players {
		assignment := assignments[index]
		result = append(result, simulation.PlayerData{
			ID:    simulation.PlayerID(player.ID),
			Team:  assignment.Team,
			Slot:  assignment.Slot,
			IsBot: player.IsBot,
			Pos:   assignment.SpawnPosition,
		})
	}
	return result
}
