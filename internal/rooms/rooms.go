package rooms

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

type RoomStatus string

const (
	RoomStatusWaiting RoomStatus = "waiting"
	RoomStatusStarted RoomStatus = "started"
)

type MatchStatus string

const (
	MatchStatusMatched  MatchStatus = "matched"
	MatchStatusLoading  MatchStatus = "loading"
	MatchStatusStarting MatchStatus = "starting"
	MatchStatusStarted  MatchStatus = "started"
)

const (
	defaultWaitingRoomIdleTTL    = 10 * time.Minute
	defaultDisconnectedRoomTTL   = 5 * time.Minute
	defaultHardRoomLifetime      = time.Hour
	defaultRoomWebSocketCloseMsg = "room expired"
	defaultRoomDebugDeleteMsg    = "room deleted"
	defaultMatchCancelMsg        = "match canceled"
	webSocketWriteTimeout        = 10 * time.Millisecond
	matchPlayerCount             = 2
	matchCountdownSeconds        = 5
)

type Store struct {
	mu             sync.Mutex
	maxActiveRooms int
	nextRoomSeq    int
	nextPlayerSeq  int
	rooms          map[string]*room
	clock          clock
	gameMap        simulation.MapData
	closed         bool
}

type StoreConfig struct {
	Map simulation.MapData
}

type room struct {
	ID              string
	Status          RoomStatus
	Players         []playerResponse
	matchStatus     MatchStatus
	readyPlayers    map[string]bool
	countdown       int
	state           *simulation.State
	pendingInputs   map[string]simulation.InputCommand
	clients         map[string]*websocket.Conn
	latestSnapshot  snapshotSummary
	createdAt       time.Time
	lastActivityAt  time.Time
	disconnectedAt  time.Time
	ticker          ticker
	stop            chan struct{}
	countdownTicker ticker
	countdownStop   chan struct{}
}

type roomListResponse struct {
	Rooms []roomResponse `json:"rooms"`
}

type roomResponse struct {
	ID             string           `json:"id"`
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
	ID   string `json:"id"`
	Team string `json:"team"`
	Slot int    `json:"slot"`
}

type matchmakingJoinResponse struct {
	Room          roomResponse   `json:"room"`
	Player        playerResponse `json:"player"`
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

type clock interface {
	Now() time.Time
	NewTicker(duration time.Duration) ticker
}

type ticker interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

type realTicker struct {
	*time.Ticker
}

func NewStore(maxActiveRooms int) *Store {
	return newStore(maxActiveRooms, nil, simulation.MapData{})
}

func NewStoreWithClock(maxActiveRooms int, clock clock) *Store {
	return newStore(maxActiveRooms, clock, simulation.MapData{})
}

func NewStoreWithConfig(maxActiveRooms int, config StoreConfig) *Store {
	return newStore(maxActiveRooms, nil, config.Map)
}

func newStore(maxActiveRooms int, clock clock, configuredMap simulation.MapData) *Store {
	if maxActiveRooms <= 0 {
		maxActiveRooms = 5
	}
	if clock == nil {
		clock = realClock{}
	}
	gameMap, err := simulation.ResolveMapData(configuredMap)
	if err != nil {
		gameMap = simulation.StaticMapFixture()
	}

	return &Store{
		maxActiveRooms: maxActiveRooms,
		rooms:          make(map[string]*room),
		clock:          clock,
		gameMap:        gameMap,
	}
}

func (realClock) NewTicker(duration time.Duration) ticker {
	return realTicker{Ticker: time.NewTicker(duration)}
}

func (realClock) Now() time.Time {
	return time.Now()
}

func (t realTicker) C() <-chan time.Time {
	return t.Ticker.C
}

func Handler(store *Store) http.Handler {
	if store == nil {
		store = NewStore(5)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/matchmaking/join" {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
				return
			}
			joined, err := store.joinMatchmaking()
			if err != nil {
				writeError(w, http.StatusConflict, "room_cap_reached", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, joined)
			return
		}

		if r.URL.Path == "/rooms" {
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, http.StatusOK, store.listRooms())
			case http.MethodPost:
				created, err := store.createRoom()
				if err != nil {
					writeError(w, http.StatusConflict, "room_cap_reached", err.Error())
					return
				}
				writeJSON(w, http.StatusCreated, created)
			case http.MethodDelete:
				writeJSON(w, http.StatusOK, store.clearRooms())
			default:
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			}
			return
		}

		if !strings.HasPrefix(r.URL.Path, "/rooms/") {
			writeError(w, http.StatusNotFound, "not_found", "route not found")
			return
		}

		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/rooms/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			writeError(w, http.StatusNotFound, "room_not_found", "room not found")
			return
		}
		roomID := parts[0]

		if len(parts) == 3 && parts[1] == "players" {
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
				return
			}
			store.handleWebSocket(w, r, roomID, parts[2])
			return
		}

		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				found, ok := store.getRoom(roomID)
				if !ok {
					writeError(w, http.StatusNotFound, "room_not_found", "room not found")
					return
				}
				writeJSON(w, http.StatusOK, found)
			case http.MethodDelete:
				deleted, ok := store.deleteRoom(roomID)
				if !ok {
					writeError(w, http.StatusNotFound, "room_not_found", "room not found")
					return
				}
				writeJSON(w, http.StatusOK, deleted)
			default:
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
				return
			}
			return
		}

		if len(parts) != 2 {
			writeError(w, http.StatusNotFound, "not_found", "route not found")
			return
		}

		switch parts[1] {
		case "players":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
				return
			}
			player, err := store.addPlayer(roomID)
			if err != nil {
				status := http.StatusNotFound
				code := "room_not_found"
				if err.Error() == "room full" {
					status = http.StatusConflict
					code = "room_full"
				}
				writeError(w, status, code, err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, player)
		case "start":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
				return
			}
			started, err := store.startRoom(roomID)
			if err != nil {
				status := http.StatusConflict
				code := "room_has_no_players"
				if err.Error() == "room not found" {
					status = http.StatusNotFound
					code = "room_not_found"
				}
				writeError(w, status, code, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, started)
		default:
			writeError(w, http.StatusNotFound, "not_found", "route not found")
		}
	})
}

func (s *Store) listRooms() roomListResponse {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	rooms := make([]roomResponse, 0, len(s.rooms))
	for _, room := range s.rooms {
		rooms = append(rooms, room.toResponse(s.gameMap))
	}
	return roomListResponse{Rooms: rooms}
}

func (s *Store) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true

	var resources roomResources
	for _, room := range s.rooms {
		resources.add(room)
	}
	s.mu.Unlock()

	resources.close("store closed")
}

func (s *Store) createRoom() (roomResponse, error) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.rooms) >= s.maxActiveRooms {
		return roomResponse{}, errString("active room cap reached")
	}

	room := s.createRoomLocked()
	return room.toResponse(s.gameMap), nil
}

func (s *Store) clearRooms() clearRoomsResponse {
	s.cleanupExpired()

	s.mu.Lock()
	deleted := len(s.rooms)
	var resources roomResources
	for _, room := range s.rooms {
		resources.add(room)
	}
	s.rooms = make(map[string]*room)
	s.mu.Unlock()

	resources.close(defaultRoomDebugDeleteMsg)
	return clearRoomsResponse{Deleted: deleted}
}

func (s *Store) deleteRoom(roomID string) (clearRoomsResponse, bool) {
	s.cleanupExpired()

	s.mu.Lock()
	room, ok := s.rooms[roomID]
	if !ok {
		s.mu.Unlock()
		return clearRoomsResponse{}, false
	}
	delete(s.rooms, roomID)
	var resources roomResources
	resources.add(room)
	s.mu.Unlock()

	resources.close(defaultRoomDebugDeleteMsg)
	return clearRoomsResponse{Deleted: 1}, true
}

func (s *Store) getRoom(roomID string) (roomResponse, bool) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return roomResponse{}, false
	}
	return room.toResponse(s.gameMap), true
}

func (s *Store) addPlayer(roomID string) (playerResponse, error) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return playerResponse{}, errString("room not found")
	}
	if len(room.Players) >= s.gameMap.MaxPlayers {
		return playerResponse{}, errString("room full")
	}

	return s.addPlayerLocked(room), nil
}

func (s *Store) joinMatchmaking() (matchmakingJoinResponse, error) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	room := s.findWaitingRoomWithCapacity()
	if room == nil {
		if len(s.rooms) >= s.maxActiveRooms {
			return matchmakingJoinResponse{}, errString("active room cap reached")
		}
		room = s.createRoomLocked()
	}

	player := s.addPlayerLocked(room)
	if len(room.Players) == matchPlayerCount {
		room.matchStatus = MatchStatusMatched
		room.readyPlayers = make(map[string]bool)
		room.lastActivityAt = s.clock.Now()
	}

	roomResponse := room.toResponse(s.gameMap)
	return matchmakingJoinResponse{
		Room:          roomResponse,
		Player:        player,
		WebSocketPath: webSocketPath(roomResponse.ID, player.ID),
	}, nil
}

func (s *Store) findWaitingRoomWithCapacity() *room {
	for _, room := range s.rooms {
		if room.Status == RoomStatusWaiting && room.matchStatus == "" && len(room.Players) < s.gameMap.MaxPlayers && len(room.Players) < matchPlayerCount {
			return room
		}
	}
	return nil
}

func (s *Store) startRoom(roomID string) (roomResponse, error) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return roomResponse{}, errString("room not found")
	}
	if len(room.Players) == 0 {
		return roomResponse{}, errString("room has no players")
	}

	s.startRoomLocked(room)
	return room.toResponse(s.gameMap), nil
}

func (s *Store) createRoomLocked() *room {
	now := s.clock.Now()
	s.nextRoomSeq++
	room := &room{
		ID:             "room-" + itoa(s.nextRoomSeq),
		Status:         RoomStatusWaiting,
		pendingInputs:  make(map[string]simulation.InputCommand),
		clients:        make(map[string]*websocket.Conn),
		createdAt:      now,
		lastActivityAt: now,
	}
	s.rooms[room.ID] = room
	return room
}

func (s *Store) addPlayerLocked(room *room) playerResponse {
	s.nextPlayerSeq++
	playerIndex := len(room.Players)
	player := playerResponse{
		ID:   "player-" + itoa(s.nextPlayerSeq),
		Team: teamForIndex(playerIndex),
		Slot: playerIndex / 2,
	}
	room.Players = append(room.Players, player)
	room.lastActivityAt = s.clock.Now()
	return player
}

func (s *Store) startRoomLocked(room *room) {
	now := s.clock.Now()
	s.stopMatchCountdownLocked(room)
	room.Status = RoomStatusStarted
	room.matchStatus = MatchStatusStarted
	room.countdown = 0
	room.lastActivityAt = now
	if len(room.clients) == 0 {
		room.disconnectedAt = now
	} else {
		room.disconnectedAt = time.Time{}
	}
	if room.state == nil {
		room.state = simulation.NewStateWithConfig(simulationPlayers(room.Players, s.gameMap), simulation.Config{Map: s.gameMap})
	}
	if room.ticker == nil {
		room.ticker = s.clock.NewTicker(time.Second / time.Duration(simulation.TickRate))
		room.stop = make(chan struct{})
		go s.runRoom(room.ID, room.ticker, room.stop)
	}
}

func (s *Store) handleWebSocket(w http.ResponseWriter, r *http.Request, roomID string, playerID string) {
	if err := s.reserveClient(roomID, playerID); err != nil {
		status := http.StatusConflict
		code := "player_already_connected"
		if err.Error() == "room not found" {
			status = http.StatusNotFound
			code = "room_not_found"
		}
		if err.Error() == "player not found" {
			status = http.StatusNotFound
			code = "player_not_found"
		}
		writeError(w, status, code, err.Error())
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.releaseClient(roomID, playerID)
		return
	}
	s.attachClient(roomID, playerID, conn)
	defer func() {
		s.releaseClient(roomID, playerID)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		_, payload, err := conn.Read(r.Context())
		if err != nil {
			return
		}

		var envelope inputEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			writeWebSocketJSON(conn, errorMessage{
				Type: "error",
				Error: apiError{
					Code:    "invalid_input",
					Message: "invalid input",
				},
			})
			continue
		}
		if envelope.Type == "ready" {
			s.markClientReady(roomID, playerID)
			continue
		}
		if envelope.Type != "" {
			writeWebSocketJSON(conn, errorMessage{
				Type: "error",
				Error: apiError{
					Code:    "invalid_input",
					Message: "invalid input",
				},
			})
			continue
		}

		var input inputMessage
		if err := json.Unmarshal(payload, &input); err != nil {
			writeWebSocketJSON(conn, errorMessage{
				Type: "error",
				Error: apiError{
					Code:    "invalid_input",
					Message: "invalid input",
				},
			})
			continue
		}
		s.setInput(roomID, playerID, input)
	}
}

func (s *Store) reserveClient(roomID string, playerID string) error {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return errString("room not found")
	}
	if !room.hasPlayer(playerID) {
		return errString("player not found")
	}
	if _, ok := room.clients[playerID]; ok {
		return errString("player already connected")
	}
	room.clients[playerID] = nil
	room.lastActivityAt = s.clock.Now()
	room.disconnectedAt = time.Time{}
	return nil
}

func (s *Store) attachClient(roomID string, playerID string, conn *websocket.Conn) {
	var deliveries []webSocketDelivery

	s.mu.Lock()

	room, ok := s.rooms[roomID]
	if !ok {
		s.mu.Unlock()
		return
	}
	room.clients[playerID] = conn
	room.lastActivityAt = s.clock.Now()
	room.disconnectedAt = time.Time{}
	if room.hasPreStartMatch() && room.matchStatus == MatchStatusMatched && room.allMatchClientsAttached() {
		room.matchStatus = MatchStatusLoading
		deliveries = append(deliveries, room.readyEventDeliveries(s.gameMap)...)
		if room.allMatchPlayersReady() {
			s.startMatchCountdownLocked(room)
			deliveries = append(deliveries, room.matchSnapshotDeliveries(MatchStatusStarting, room.countdown)...)
		}
	}
	s.mu.Unlock()

	writeWebSocketDeliveries(deliveries)
}

func (s *Store) releaseClient(roomID string, playerID string) {
	var resources roomResources
	shouldClose := false

	s.mu.Lock()

	room, ok := s.rooms[roomID]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(room.clients, playerID)
	delete(room.pendingInputs, playerID)
	delete(room.readyPlayers, playerID)
	if room.hasPreStartMatch() {
		delete(s.rooms, roomID)
		resources.add(room)
		shouldClose = true
	}
	if room.Status == RoomStatusStarted && len(room.clients) == 0 {
		room.disconnectedAt = s.clock.Now()
	}
	s.mu.Unlock()

	if shouldClose {
		resources.close(defaultMatchCancelMsg)
	}
}

func (s *Store) setInput(roomID string, playerID string, input inputMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok || !room.hasPlayer(playerID) {
		return
	}
	room.lastActivityAt = s.clock.Now()
	room.pendingInputs[playerID] = simulation.InputCommand{
		PlayerID:      simulation.PlayerID(playerID),
		MoveDir:       input.MoveDir,
		AttackDir:     input.AttackDir,
		PressedAttack: input.PressedAttack,
	}
}

func (s *Store) markClientReady(roomID string, playerID string) {
	var deliveries []webSocketDelivery

	s.mu.Lock()
	room, ok := s.rooms[roomID]
	if !ok || !room.hasPlayer(playerID) || !room.hasPreStartMatch() {
		s.mu.Unlock()
		return
	}
	if room.readyPlayers == nil {
		room.readyPlayers = make(map[string]bool)
	}
	room.readyPlayers[playerID] = true
	room.lastActivityAt = s.clock.Now()
	if room.matchStatus == MatchStatusLoading && room.allMatchPlayersReady() {
		s.startMatchCountdownLocked(room)
		deliveries = append(deliveries, room.matchSnapshotDeliveries(MatchStatusStarting, room.countdown)...)
	}
	s.mu.Unlock()

	writeWebSocketDeliveries(deliveries)
}

func (s *Store) startMatchCountdownLocked(room *room) {
	room.matchStatus = MatchStatusStarting
	room.countdown = matchCountdownSeconds
	room.countdownTicker = s.clock.NewTicker(time.Second)
	room.countdownStop = make(chan struct{})
	go s.runMatchCountdown(room.ID, room.countdownTicker, room.countdownStop)
}

func (s *Store) stopMatchCountdownLocked(room *room) {
	if room.countdownTicker != nil {
		room.countdownTicker.Stop()
		room.countdownTicker = nil
	}
	if room.countdownStop != nil {
		close(room.countdownStop)
		room.countdownStop = nil
	}
}

func (s *Store) runRoom(roomID string, ticker ticker, stop <-chan struct{}) {
	for {
		select {
		case <-ticker.C():
			s.tickRoom(roomID)
		case <-stop:
			return
		}
	}
}

func (s *Store) runMatchCountdown(roomID string, ticker ticker, stop <-chan struct{}) {
	for {
		select {
		case <-ticker.C():
			if s.tickMatchCountdown(roomID, ticker) {
				return
			}
		case <-stop:
			return
		}
	}
}

func (s *Store) tickMatchCountdown(roomID string, countdownTicker ticker) bool {
	var deliveries []webSocketDelivery

	s.mu.Lock()
	room, ok := s.rooms[roomID]
	if !ok || room.matchStatus != MatchStatusStarting {
		s.mu.Unlock()
		return true
	}
	if room.countdown > 1 {
		room.countdown--
		room.lastActivityAt = s.clock.Now()
		deliveries = append(deliveries, room.matchSnapshotDeliveries(MatchStatusStarting, room.countdown)...)
		s.mu.Unlock()
		writeWebSocketDeliveries(deliveries)
		return false
	}

	room.countdown = 0
	s.startRoomLocked(room)
	deliveries = append(deliveries, room.matchSnapshotDeliveries(MatchStatusStarted, 0)...)
	s.mu.Unlock()

	countdownTicker.Stop()
	writeWebSocketDeliveries(deliveries)
	return true
}

func (s *Store) tickRoom(roomID string) {
	s.cleanupExpired()

	s.mu.Lock()

	room, ok := s.rooms[roomID]
	if !ok || room.Status != RoomStatusStarted || room.state == nil {
		s.mu.Unlock()
		return
	}

	inputs := make([]simulation.InputCommand, 0, len(room.pendingInputs))
	for _, input := range room.pendingInputs {
		inputs = append(inputs, input)
	}
	room.pendingInputs = make(map[string]simulation.InputCommand)
	snapshot := room.state.Step(inputs)
	room.latestSnapshot = snapshotSummaryFromSnapshot(snapshot)
	message := roomSnapshotMessage{Type: "snapshot", Snapshot: roomSnapshotFromSimulation(snapshot, MatchStatusStarted)}

	clients := make([]*websocket.Conn, 0, len(room.clients))
	for _, conn := range room.clients {
		if conn != nil {
			clients = append(clients, conn)
		}
	}
	s.mu.Unlock()

	for _, conn := range clients {
		writeWebSocketJSON(conn, message)
	}
}

func (s *Store) cleanupExpired() {
	now := s.clock.Now()

	s.mu.Lock()
	var resources roomResources
	for id, room := range s.rooms {
		if !room.isExpired(now) {
			continue
		}
		delete(s.rooms, id)
		resources.add(room)
	}
	s.mu.Unlock()

	resources.close(defaultRoomWebSocketCloseMsg)
}

type roomResources struct {
	tickers []ticker
	stops   []chan struct{}
	conns   []*websocket.Conn
}

func (r *roomResources) add(room *room) {
	if room.countdownTicker != nil {
		r.tickers = append(r.tickers, room.countdownTicker)
		room.countdownTicker = nil
	}
	if room.countdownStop != nil {
		r.stops = append(r.stops, room.countdownStop)
		room.countdownStop = nil
	}
	if room.ticker != nil {
		r.tickers = append(r.tickers, room.ticker)
		room.ticker = nil
	}
	if room.stop != nil {
		r.stops = append(r.stops, room.stop)
		room.stop = nil
	}
	for _, conn := range room.clients {
		if conn != nil {
			r.conns = append(r.conns, conn)
		}
	}
	room.clients = nil
}

func (r roomResources) close(reason string) {
	for _, ticker := range r.tickers {
		ticker.Stop()
	}
	for _, stop := range r.stops {
		close(stop)
	}
	for _, conn := range r.conns {
		_ = conn.Close(websocket.StatusNormalClosure, reason)
	}
}

type webSocketDelivery struct {
	conn    *websocket.Conn
	message any
}

func writeWebSocketDeliveries(deliveries []webSocketDelivery) {
	for _, delivery := range deliveries {
		if delivery.conn != nil {
			writeWebSocketJSON(delivery.conn, delivery.message)
		}
	}
}

func writeWebSocketJSON(conn *websocket.Conn, message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), webSocketWriteTimeout)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, payload)
}

func (r *room) toResponse(gameMap simulation.MapData) roomResponse {
	players := make([]playerResponse, len(r.Players))
	copy(players, r.Players)
	latestSnapshot := r.latestSnapshot
	if latestSnapshot == (snapshotSummary{}) {
		latestSnapshot.PlayerCount = len(players)
	}
	return roomResponse{
		ID:             r.ID,
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

func (r *room) hasPlayer(playerID string) bool {
	for _, player := range r.Players {
		if player.ID == playerID {
			return true
		}
	}
	return false
}

func webSocketPath(roomID string, playerID string) string {
	return "/rooms/" + roomID + "/players/" + playerID
}

func (r *room) isExpired(now time.Time) bool {
	if !r.createdAt.IsZero() && !now.Before(r.createdAt.Add(defaultHardRoomLifetime)) {
		return true
	}
	if len(r.clients) > 0 {
		return false
	}
	if r.Status == RoomStatusWaiting {
		return !now.Before(r.lastActivityAt.Add(defaultWaitingRoomIdleTTL))
	}
	if r.Status == RoomStatusStarted && !r.disconnectedAt.IsZero() {
		return !now.Before(r.disconnectedAt.Add(defaultDisconnectedRoomTTL))
	}
	return false
}

func (r *room) hasPreStartMatch() bool {
	return r.Status != RoomStatusStarted && r.matchStatus != ""
}

func (r *room) allMatchClientsAttached() bool {
	if len(r.Players) < matchPlayerCount {
		return false
	}
	for _, player := range r.Players {
		conn, ok := r.clients[player.ID]
		if !ok || conn == nil {
			return false
		}
	}
	return true
}

func (r *room) allMatchPlayersReady() bool {
	if len(r.Players) < matchPlayerCount {
		return false
	}
	for _, player := range r.Players {
		if !r.readyPlayers[player.ID] {
			return false
		}
	}
	return true
}

func (r *room) matchSnapshotDeliveries(status MatchStatus, countdown int) []webSocketDelivery {
	message := r.matchSnapshotMessage(status, countdown)
	deliveries := make([]webSocketDelivery, 0, len(r.clients))
	for _, conn := range r.clients {
		if conn != nil {
			deliveries = append(deliveries, webSocketDelivery{
				conn:    conn,
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

func (r *room) readyEventDeliveries(gameMap simulation.MapData) []webSocketDelivery {
	message := readyEventMessage{
		Type:    "Ready",
		Map:     mapResponseFromSimulation(gameMap),
		Players: readyEventPlayers(r.Players, gameMap),
	}
	deliveries := make([]webSocketDelivery, 0, len(r.clients))
	for _, conn := range r.clients {
		if conn != nil {
			deliveries = append(deliveries, webSocketDelivery{
				conn:    conn,
				message: message,
			})
		}
	}
	return deliveries
}

type inputMessage struct {
	MoveDir       simulation.Vector2 `json:"MoveDir"`
	AttackDir     simulation.Vector2 `json:"AttackDir"`
	PressedAttack bool               `json:"PressedAttack"`
}

type inputEnvelope struct {
	Type string `json:"Type"`
}

type snapshotMessage struct {
	Type     string              `json:"Type"`
	Snapshot simulation.Snapshot `json:"Snapshot"`
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
	SpawnPosition simulation.Vector2 `json:"SpawnPosition"`
}

func roomSnapshotFromSimulation(snapshot simulation.Snapshot, status MatchStatus) roomSnapshot {
	return roomSnapshot{
		Status:      status,
		Tick:        snapshot.Tick,
		Players:     snapshot.Players,
		Projectiles: snapshot.Projectiles,
	}
}

func readyEventPlayers(players []playerResponse, gameMap simulation.MapData) []readyEventPlayer {
	spawnedPlayers := simulationPlayers(players, gameMap)
	result := make([]readyEventPlayer, 0, len(spawnedPlayers))
	for _, player := range spawnedPlayers {
		result = append(result, readyEventPlayer{
			ID:            string(player.ID),
			Team:          string(player.Team),
			Slot:          player.Slot,
			SpawnPosition: player.Pos,
		})
	}
	return result
}

type errorMessage struct {
	Type  string   `json:"Type"`
	Error apiError `json:"Error"`
}

func simulationPlayers(players []playerResponse, gameMap simulation.MapData) []simulation.PlayerData {
	result := make([]simulation.PlayerData, 0, len(players))
	spawns := spawnPoints(gameMap)
	for index, player := range players {
		result = append(result, simulation.PlayerData{
			ID:   simulation.PlayerID(player.ID),
			Team: simulation.Team(player.Team),
			Slot: player.Slot,
			Pos:  spawnPosition(gameMap, spawns, index, player),
		})
	}
	return result
}

func spawnPoints(gameMap simulation.MapData) []simulation.Vector2 {
	spawns := make([]simulation.Vector2, 0)
	for y, row := range gameMap.Map {
		for x, tile := range row {
			if tile == simulation.TileSpawnPoint {
				spawns = append(spawns, gameMap.WorldPos(x, y))
			}
		}
	}
	return spawns
}

func spawnPosition(gameMap simulation.MapData, spawns []simulation.Vector2, index int, player playerResponse) simulation.Vector2 {
	if len(spawns) > 0 {
		return spawns[index%len(spawns)]
	}
	if player.Team == "blue" {
		return gameMap.WorldPos(3, 3)
	}
	return gameMap.WorldPos(1, 1)
}

func teamForIndex(index int) string {
	if index%2 == 0 {
		return "red"
	}
	return "blue"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, errorResponse{Error: apiError{Code: code, Message: message}})
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}

	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
