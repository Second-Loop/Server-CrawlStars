package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

type clientReservation struct {
	room                   *room
	playerID               string
	reservedAt             time.Time
	previousLastActivityAt time.Time
}

func (s *Store) handleWebSocket(w http.ResponseWriter, r *http.Request, roomID string, playerID string) {
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	var tokens []string
	if queryErr == nil {
		tokens = query["token"]
	}
	reservation, err := s.reserveClient(roomID, playerID, tokens)
	if err != nil {
		status := http.StatusConflict
		code := "player_already_connected"
		if errors.Is(err, ErrRoomNotFound) {
			status = http.StatusNotFound
			code = "room_not_found"
		}
		if errors.Is(err, ErrPlayerNotFound) {
			status = http.StatusNotFound
			code = "player_not_found"
		}
		if errors.Is(err, ErrUnauthorized) {
			status = http.StatusUnauthorized
			code = "unauthorized"
		}
		writeError(w, status, code, err.Error())
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.rollbackClientReservation(reservation)
		return
	}
	if !s.attachClient(reservation, conn) {
		s.rollbackClientReservation(reservation)
		_ = conn.Close(websocket.StatusGoingAway, "room unavailable")
		return
	}
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

func (s *Store) reserveClient(roomID string, playerID string, tokens []string) (*clientReservation, error) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrRoomNotFound
	}
	room, ok := s.rooms[roomID]
	if !ok {
		return nil, ErrRoomNotFound
	}
	if !room.hasPlayer(playerID) {
		return nil, ErrPlayerNotFound
	}
	if len(tokens) != 1 || tokens[0] == "" || !room.authenticatePlayer(playerID, tokens[0]) {
		return nil, ErrUnauthorized
	}
	if _, ok := room.clients[playerID]; ok {
		return nil, ErrPlayerAlreadyConnected
	}
	if _, ok := room.reservations[playerID]; ok {
		return nil, ErrPlayerAlreadyConnected
	}
	reservedAt := s.clock.Now()
	reservation := &clientReservation{
		room:                   room,
		playerID:               playerID,
		reservedAt:             reservedAt,
		previousLastActivityAt: room.lastActivityAt,
	}
	room.reservations[playerID] = reservation
	room.lastActivityAt = reservedAt
	return reservation, nil
}

func (s *Store) rollbackClientReservation(reservation *clientReservation) {
	if reservation == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[reservation.room.ID]
	if !ok || room != reservation.room || room.reservations == nil || room.reservations[reservation.playerID] != reservation {
		return
	}
	delete(room.reservations, reservation.playerID)
	if len(room.reservations) == 0 && room.lastActivityAt.Equal(reservation.reservedAt) {
		room.lastActivityAt = reservation.previousLastActivityAt
	}
}

func (s *Store) attachClient(reservation *clientReservation, conn *websocket.Conn) bool {
	var deliveries []webSocketDelivery

	s.mu.Lock()

	if s.closed || reservation == nil {
		s.mu.Unlock()
		return false
	}
	room, ok := s.rooms[reservation.room.ID]
	if !ok || room != reservation.room || room.clients == nil || room.reservations == nil || room.reservations[reservation.playerID] != reservation {
		s.mu.Unlock()
		return false
	}
	delete(room.reservations, reservation.playerID)
	room.clients[reservation.playerID] = conn
	room.lastActivityAt = s.clock.Now()
	room.disconnectedAt = time.Time{}
	if room.hasPreStartMatch() && room.matchStatus == MatchStatusMatched && room.allMatchClientsAttached(s.matchCapacity()) {
		room.matchStatus = MatchStatusLoading
		deliveries = append(deliveries, room.readyEventDeliveries(s.gameConfig)...)
		if room.allMatchPlayersReady(s.matchCapacity()) {
			s.startMatchCountdownLocked(room)
			deliveries = append(deliveries, room.matchSnapshotDeliveries(MatchStatusStarting, room.countdown)...)
		}
	}
	s.mu.Unlock()

	writeWebSocketDeliveries(deliveries)
	return true
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
	if room.matchStatus == MatchStatusLoading && room.allMatchPlayersReady(s.matchCapacity()) {
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
		s.mu.Unlock()
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

	var resources roomResources
	var deliveries []webSocketDelivery
	gameEnded := false

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

	for _, conn := range room.clients {
		if conn != nil {
			deliveries = append(deliveries, webSocketDelivery{conn: conn, message: message})
		}
	}
	results := calculateGameEndResults(s.gameConfig, snapshot)
	if len(results) > 0 {
		deliveries = append(deliveries, room.gameEndDeliveries(results)...)
		delete(s.rooms, roomID)
		resources.add(room)
		gameEnded = true
	}
	s.mu.Unlock()

	writeWebSocketDeliveries(deliveries)
	if gameEnded {
		resources.close(defaultGameEndCloseMsg)
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

func (r *room) hasPreStartMatch() bool {
	return r.Status != RoomStatusStarted && r.matchStatus != ""
}

func (r *room) allMatchClientsAttached(matchPlayerCount int) bool {
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

func (r *room) allMatchPlayersReady(matchPlayerCount int) bool {
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
