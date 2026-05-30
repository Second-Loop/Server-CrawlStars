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

type Store struct {
	mu             sync.Mutex
	maxActiveRooms int
	nextRoomSeq    int
	nextPlayerSeq  int
	rooms          map[string]*room
	clock          clock
	closed         bool
}

type room struct {
	ID            string
	Status        RoomStatus
	Players       []playerResponse
	state         *simulation.State
	pendingInputs map[string]simulation.InputCommand
	clients       map[string]*websocket.Conn
	ticker        ticker
	stop          chan struct{}
}

type roomListResponse struct {
	Rooms []roomResponse `json:"rooms"`
}

type roomResponse struct {
	ID             string           `json:"id"`
	Status         RoomStatus       `json:"status"`
	Players        []playerResponse `json:"players"`
	LatestSnapshot snapshotSummary  `json:"latestSnapshot"`
}

type playerResponse struct {
	ID   string `json:"id"`
	Team string `json:"team"`
	Slot int    `json:"slot"`
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
	return NewStoreWithClock(maxActiveRooms, realClock{})
}

func NewStoreWithClock(maxActiveRooms int, clock clock) *Store {
	if maxActiveRooms <= 0 {
		maxActiveRooms = 5
	}
	if clock == nil {
		clock = realClock{}
	}

	return &Store{
		maxActiveRooms: maxActiveRooms,
		rooms:          make(map[string]*room),
		clock:          clock,
	}
}

func (realClock) NewTicker(duration time.Duration) ticker {
	return realTicker{Ticker: time.NewTicker(duration)}
}

func (t realTicker) C() <-chan time.Time {
	return t.Ticker.C
}

func Handler(store *Store) http.Handler {
	if store == nil {
		store = NewStore(5)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
				return
			}
			found, ok := store.getRoom(roomID)
			if !ok {
				writeError(w, http.StatusNotFound, "room_not_found", "room not found")
				return
			}
			writeJSON(w, http.StatusOK, found)
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
				writeError(w, http.StatusNotFound, "room_not_found", err.Error())
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
	s.mu.Lock()
	defer s.mu.Unlock()

	rooms := make([]roomResponse, 0, len(s.rooms))
	for _, room := range s.rooms {
		rooms = append(rooms, room.toResponse())
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

	var tickers []ticker
	var stops []chan struct{}
	var conns []*websocket.Conn
	for _, room := range s.rooms {
		if room.ticker != nil {
			tickers = append(tickers, room.ticker)
		}
		if room.stop != nil {
			stops = append(stops, room.stop)
			room.stop = nil
		}
		for _, conn := range room.clients {
			conns = append(conns, conn)
		}
		room.clients = nil
	}
	s.mu.Unlock()

	for _, ticker := range tickers {
		ticker.Stop()
	}
	for _, stop := range stops {
		close(stop)
	}
	for _, conn := range conns {
		_ = conn.Close(websocket.StatusNormalClosure, "store closed")
	}
}

func (s *Store) createRoom() (roomResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.rooms) >= s.maxActiveRooms {
		return roomResponse{}, errString("active room cap reached")
	}

	s.nextRoomSeq++
	room := &room{
		ID:            "room-" + itoa(s.nextRoomSeq),
		Status:        RoomStatusWaiting,
		pendingInputs: make(map[string]simulation.InputCommand),
		clients:       make(map[string]*websocket.Conn),
	}
	s.rooms[room.ID] = room
	return room.toResponse(), nil
}

func (s *Store) getRoom(roomID string) (roomResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return roomResponse{}, false
	}
	return room.toResponse(), true
}

func (s *Store) addPlayer(roomID string) (playerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return playerResponse{}, errString("room not found")
	}

	s.nextPlayerSeq++
	playerIndex := len(room.Players)
	player := playerResponse{
		ID:   "player-" + itoa(s.nextPlayerSeq),
		Team: teamForIndex(playerIndex),
		Slot: playerIndex / 2,
	}
	room.Players = append(room.Players, player)
	return player, nil
}

func (s *Store) startRoom(roomID string) (roomResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return roomResponse{}, errString("room not found")
	}
	if len(room.Players) == 0 {
		return roomResponse{}, errString("room has no players")
	}

	room.Status = RoomStatusStarted
	if room.state == nil {
		room.state = simulation.NewStateWithConfig(simulationPlayers(room.Players), simulation.Config{Map: simulation.StaticMapFixture()})
	}
	if room.ticker == nil {
		room.ticker = s.clock.NewTicker(time.Second / time.Duration(simulation.TickRate))
		room.stop = make(chan struct{})
		go s.runRoom(roomID, room.ticker, room.stop)
	}
	return room.toResponse(), nil
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

		var input inputMessage
		if err := json.Unmarshal(payload, &input); err != nil {
			continue
		}
		s.setInput(roomID, playerID, input)
	}
}

func (s *Store) reserveClient(roomID string, playerID string) error {
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
	return nil
}

func (s *Store) attachClient(roomID string, playerID string, conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return
	}
	room.clients[playerID] = conn
}

func (s *Store) releaseClient(roomID string, playerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return
	}
	delete(room.clients, playerID)
	delete(room.pendingInputs, playerID)
}

func (s *Store) setInput(roomID string, playerID string, input inputMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok || !room.hasPlayer(playerID) {
		return
	}
	room.pendingInputs[playerID] = simulation.InputCommand{
		PlayerID:      simulation.PlayerID(playerID),
		MoveDir:       input.MoveDir,
		AttackDir:     input.AttackDir,
		PressedAttack: input.PressedAttack,
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

func (s *Store) tickRoom(roomID string) {
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
	message := snapshotMessage{Type: "snapshot", Snapshot: snapshot}

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

func writeWebSocketJSON(conn *websocket.Conn, message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, payload)
}

func (r *room) toResponse() roomResponse {
	players := make([]playerResponse, len(r.Players))
	copy(players, r.Players)
	return roomResponse{
		ID:      r.ID,
		Status:  r.Status,
		Players: players,
		LatestSnapshot: snapshotSummary{
			Tick:            0,
			PlayerCount:     len(players),
			ProjectileCount: 0,
		},
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

type inputMessage struct {
	MoveDir       simulation.Vector2 `json:"MoveDir"`
	AttackDir     simulation.Vector2 `json:"AttackDir"`
	PressedAttack bool               `json:"PressedAttack"`
}

type snapshotMessage struct {
	Type     string              `json:"Type"`
	Snapshot simulation.Snapshot `json:"Snapshot"`
}

func simulationPlayers(players []playerResponse) []simulation.PlayerData {
	result := make([]simulation.PlayerData, 0, len(players))
	gameMap := simulation.StaticMapFixture()
	for _, player := range players {
		result = append(result, simulation.PlayerData{
			ID:   simulation.PlayerID(player.ID),
			Team: simulation.Team(player.Team),
			Slot: player.Slot,
			Pos:  spawnPosition(gameMap, player),
		})
	}
	return result
}

func spawnPosition(gameMap simulation.MapData, player playerResponse) simulation.Vector2 {
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
