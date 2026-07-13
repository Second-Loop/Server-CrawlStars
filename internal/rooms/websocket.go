package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

type clientConn interface {
	Read(context.Context) (websocket.MessageType, []byte, error)
	Write(context.Context, websocket.MessageType, []byte) error
	Ping(context.Context) error
	Close(websocket.StatusCode, string) error
}

type writerCommandKind uint8

const (
	writerCommandPayload writerCommandKind = iota
	writerCommandClose
)

type writerCommand struct {
	kind    writerCommandKind
	payload []byte
	reason  string
}

type clientSession struct {
	conn      clientConn
	snapshots chan []byte
	control   chan writerCommand
	done      chan struct{}
	enqueueMu sync.Mutex
	closeOnce sync.Once
	onClose   func(*clientSession)
	terminal  bool
}

func newClientSession(conn clientConn, onClose func(*clientSession)) *clientSession {
	session := &clientSession{
		conn:      conn,
		snapshots: make(chan []byte, 1),
		control:   make(chan writerCommand, 8),
		done:      make(chan struct{}),
		onClose:   onClose,
	}
	go session.writeLoop()
	return session
}

func (s *clientSession) enqueueSnapshot(payload []byte) {
	s.enqueueMu.Lock()
	defer s.enqueueMu.Unlock()
	if s.terminal {
		return
	}

	select {
	case <-s.done:
		return
	default:
	}

	select {
	case s.snapshots <- payload:
	default:
		select {
		case <-s.snapshots:
		default:
		}
		select {
		case s.snapshots <- payload:
		case <-s.done:
		}
	}
}

func (s *clientSession) enqueueControl(payload []byte) bool {
	queued, shouldClose := s.tryEnqueueControl(payload)
	if shouldClose {
		s.close(websocket.StatusGoingAway, "control queue overflow")
	}
	return queued
}

// tryEnqueueControl never closes the session, so callers may use it while
// holding room.mu and defer close/release until after unlocking the room.
func (s *clientSession) tryEnqueueControl(payload []byte) (queued bool, shouldClose bool) {
	s.enqueueMu.Lock()
	defer s.enqueueMu.Unlock()
	if s.terminal {
		return false, false
	}
	select {
	case <-s.done:
		return false, false
	default:
	}

	select {
	case s.control <- writerCommand{kind: writerCommandPayload, payload: payload}:
		return true, false
	default:
		return false, true
	}
}

func (s *clientSession) enqueueTerminal(snapshot []byte, gameEnd []byte, reason string) bool {
	s.enqueueMu.Lock()
	if s.terminal {
		s.enqueueMu.Unlock()
		return false
	}
	select {
	case <-s.done:
		s.enqueueMu.Unlock()
		return false
	default:
	}

	s.terminal = true
	select {
	case <-s.snapshots:
	default:
	}
	commands := [...]writerCommand{
		{kind: writerCommandPayload, payload: snapshot},
		{kind: writerCommandPayload, payload: gameEnd},
		{kind: writerCommandClose, reason: reason},
	}
	if cap(s.control)-len(s.control) < len(commands) {
		s.enqueueMu.Unlock()
		s.close(websocket.StatusGoingAway, "control queue overflow")
		return false
	}
	for _, command := range commands {
		s.control <- command
	}
	s.enqueueMu.Unlock()
	return true
}

func (s *clientSession) writeLoop() {
	for {
		select {
		case command := <-s.control:
			if !s.writeCommand(command) {
				return
			}
			continue
		default:
		}

		select {
		case command := <-s.control:
			if !s.writeCommand(command) {
				return
			}
		case payload := <-s.snapshots:
			if !s.writeCommand(writerCommand{kind: writerCommandPayload, payload: payload}) {
				return
			}
		case <-s.done:
			return
		}
	}
}

func (s *clientSession) writeCommand(command writerCommand) bool {
	if command.kind == writerCommandClose {
		s.close(websocket.StatusNormalClosure, command.reason)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), webSocketWriteTimeout)
	err := s.conn.Write(ctx, websocket.MessageText, command.payload)
	cancel()
	if err != nil {
		s.close(websocket.StatusGoingAway, "write failed")
		return false
	}
	return true
}

func (s *clientSession) close(code websocket.StatusCode, reason string) {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.onClose != nil {
			s.onClose(s)
		}
		if s.conn != nil {
			_ = s.conn.Close(code, reason)
		}
	})
}

type clientReservation struct {
	room     *room
	playerID string
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
	session, attached := s.attachClientSession(reservation, conn)
	if !attached {
		s.rollbackClientReservation(reservation)
		_ = conn.Close(websocket.StatusGoingAway, "room unavailable")
		return
	}
	defer func() {
		session.close(websocket.StatusNormalClosure, "")
	}()

	for {
		_, payload, err := session.conn.Read(r.Context())
		if err != nil {
			return
		}

		var envelope inputEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			if !enqueueControlMessage(session, errorMessage{
				Type: "error",
				Error: apiError{
					Code:    "invalid_input",
					Message: "invalid input",
				},
			}) {
				return
			}
			continue
		}
		if envelope.Type == "ready" {
			s.markClientReady(roomID, playerID, session)
			continue
		}
		if envelope.Type != "" {
			if !enqueueControlMessage(session, errorMessage{
				Type: "error",
				Error: apiError{
					Code:    "invalid_input",
					Message: "invalid input",
				},
			}) {
				return
			}
			continue
		}

		var input inputMessage
		if err := json.Unmarshal(payload, &input); err != nil {
			if !enqueueControlMessage(session, errorMessage{
				Type: "error",
				Error: apiError{
					Code:    "invalid_input",
					Message: "invalid input",
				},
			}) {
				return
			}
			continue
		}
		s.setInput(roomID, playerID, input, session)
	}
}

func (s *Store) reserveClient(roomID string, playerID string, tokens []string) (*clientReservation, error) {
	if s.isClosed() {
		return nil, ErrRoomNotFound
	}
	room := s.lookupRoom(roomID)
	if room == nil {
		return nil, ErrRoomNotFound
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.removed {
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
	reservation := &clientReservation{
		room:     room,
		playerID: playerID,
	}
	room.reservations[playerID] = reservation
	return reservation, nil
}

func (s *Store) rollbackClientReservation(reservation *clientReservation) {
	if reservation == nil {
		return
	}

	room := reservation.room
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.removed || room.reservations == nil || room.reservations[reservation.playerID] != reservation {
		return
	}
	delete(room.reservations, reservation.playerID)
}

func (s *Store) attachClient(reservation *clientReservation, conn clientConn) bool {
	_, attached := s.attachClientSession(reservation, conn)
	return attached
}

func (s *Store) attachClientSession(reservation *clientReservation, conn clientConn) (*clientSession, bool) {
	var deliveries []webSocketDelivery

	if reservation == nil || s.isClosed() {
		return nil, false
	}
	room := reservation.room
	room.mu.Lock()
	if room.removed || room.clients == nil || room.reservations == nil || room.reservations[reservation.playerID] != reservation {
		room.mu.Unlock()
		return nil, false
	}
	session := newClientSession(conn, func(expected *clientSession) {
		s.releaseClient(reservation, expected)
	})
	delete(room.reservations, reservation.playerID)
	room.clients[reservation.playerID] = session
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
	failedSessions := tryEnqueueWebSocketDeliveries(deliveries)
	room.mu.Unlock()

	closeClientSessions(failedSessions, "control delivery failed")
	return session, true
}

func (s *Store) releaseClient(reservation *clientReservation, expectedSession *clientSession) {
	var resources roomResources
	shouldClose := false

	if reservation == nil || reservation.room == nil {
		return
	}
	room := reservation.room
	playerID := reservation.playerID
	room.mu.Lock()
	if room.removed {
		room.mu.Unlock()
		return
	}
	currentSession, connected := room.clients[playerID]
	if !connected || currentSession != expectedSession {
		room.mu.Unlock()
		return
	}
	delete(room.clients, playerID)
	delete(room.pendingInputs, playerID)
	delete(room.readyPlayers, playerID)
	var playerIDs []string
	if room.hasPreStartMatch() {
		playerIDs, shouldClose = resources.removeRoomLocked(room)
	}
	if room.Status == RoomStatusStarted && len(room.clients) == 0 {
		room.disconnectedAt = s.clock.Now()
	}
	room.mu.Unlock()

	if shouldClose {
		if s.deleteRoomIfSame(room.ID, room) {
			s.releasePlayerIDs(playerIDs)
		}
		resources.close(defaultMatchCancelMsg)
	}
}

func (s *Store) setInput(roomID string, playerID string, input inputMessage, expectedSession *clientSession) {
	room := s.lookupRoom(roomID)
	if room == nil {
		return
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.removed || !room.hasPlayer(playerID) || expectedSession == nil || room.clients[playerID] != expectedSession {
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

func (s *Store) markClientReady(roomID string, playerID string, expectedSession *clientSession) {
	var deliveries []webSocketDelivery

	room := s.lookupRoom(roomID)
	if room == nil {
		return
	}
	room.mu.Lock()
	if room.removed || !room.hasPlayer(playerID) || !room.hasPreStartMatch() || expectedSession == nil || room.clients[playerID] != expectedSession {
		room.mu.Unlock()
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
	failedSessions := tryEnqueueWebSocketDeliveries(deliveries)
	room.mu.Unlock()

	closeClientSessions(failedSessions, "control delivery failed")
}

func (s *Store) startMatchCountdownLocked(room *room) {
	room.matchStatus = MatchStatusStarting
	room.countdown = matchCountdownSeconds
	room.countdownTicker = s.clock.NewTicker(time.Second)
	room.countdownStop = make(chan struct{})
	go s.runMatchCountdown(room, room.countdownTicker, room.countdownStop)
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

func (s *Store) runRoom(room *room, ticker ticker, stop <-chan struct{}) {
	for {
		select {
		case <-ticker.C():
			s.tickRoomState(room)
		case <-stop:
			return
		}
	}
}

func (s *Store) runMatchCountdown(room *room, ticker ticker, stop <-chan struct{}) {
	for {
		select {
		case <-ticker.C():
			if s.tickMatchCountdownRoom(room, ticker) {
				return
			}
		case <-stop:
			return
		}
	}
}

func (s *Store) tickMatchCountdown(roomID string, countdownTicker ticker) bool {
	room := s.lookupRoom(roomID)
	if room == nil {
		return true
	}
	return s.tickMatchCountdownRoom(room, countdownTicker)
}

func (s *Store) tickMatchCountdownRoom(room *room, countdownTicker ticker) bool {
	var deliveries []webSocketDelivery

	room.mu.Lock()
	if room.removed || room.countdownTicker != countdownTicker || room.matchStatus != MatchStatusStarting {
		room.mu.Unlock()
		return true
	}
	if room.countdown > 1 {
		room.countdown--
		room.lastActivityAt = s.clock.Now()
		room.mu.Unlock()
		return false
	}

	room.countdown = 0
	s.startRoomLocked(room)
	deliveries = append(deliveries, room.matchSnapshotDeliveries(MatchStatusStarted, 0)...)
	failedSessions := tryEnqueueWebSocketDeliveries(deliveries)
	room.mu.Unlock()

	closeClientSessions(failedSessions, "control delivery failed")
	return true
}

func (s *Store) tickRoom(roomID string) {
	room := s.lookupRoom(roomID)
	if room == nil {
		return
	}
	s.tickRoomState(room)
}

func (s *Store) tickRoomState(room *room) {
	var resources roomResources
	var deliveries []webSocketDelivery
	var snapshotSessions []*clientSession
	var snapshotPayload []byte
	gameEnded := false

	room.mu.Lock()
	if room.removed || room.Status != RoomStatusStarted || room.state == nil {
		room.mu.Unlock()
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

	for _, session := range room.clients {
		if session != nil {
			snapshotSessions = append(snapshotSessions, session)
		}
	}
	results := calculateGameEndResults(s.gameConfig, snapshot)
	var playerIDs []string
	if len(results) > 0 {
		snapshotPayload, _ = marshalMessage(message)
		deliveries = append(deliveries, room.gameEndDeliveries(results)...)
		room.clients = nil
		playerIDs, gameEnded = resources.removeRoomLocked(room)
	} else {
		enqueueSnapshotMessage(snapshotSessions, message)
	}
	room.mu.Unlock()

	if gameEnded {
		if s.deleteRoomIfSame(room.ID, room) {
			s.releasePlayerIDs(playerIDs)
		}
	}
	if gameEnded {
		for _, delivery := range deliveries {
			gameEndPayload, err := marshalMessage(delivery.message)
			if err != nil {
				delivery.session.close(websocket.StatusGoingAway, "message marshal failed")
				continue
			}
			delivery.session.enqueueTerminal(snapshotPayload, gameEndPayload, defaultGameEndCloseMsg)
		}
		resources.close(defaultGameEndCloseMsg)
		return
	}
}

type webSocketDelivery struct {
	session *clientSession
	message any
}

// tryEnqueueWebSocketDeliveries only performs non-blocking channel operations.
// Callers hold room.mu so lifecycle control ordering is fixed before the next
// countdown or gameplay transition can acquire the room.
func tryEnqueueWebSocketDeliveries(deliveries []webSocketDelivery) []*clientSession {
	var failedSessions []*clientSession
	for _, delivery := range deliveries {
		if delivery.session == nil {
			continue
		}
		payload, err := marshalMessage(delivery.message)
		if err != nil {
			failedSessions = append(failedSessions, delivery.session)
			continue
		}
		_, shouldClose := delivery.session.tryEnqueueControl(payload)
		if shouldClose {
			failedSessions = append(failedSessions, delivery.session)
		}
	}
	return failedSessions
}

func closeClientSessions(sessions []*clientSession, reason string) {
	for _, session := range sessions {
		session.close(websocket.StatusGoingAway, reason)
	}
}

func marshalMessage(message any) ([]byte, error) {
	return json.Marshal(message)
}

func enqueueSnapshotMessage(sessions []*clientSession, message any) bool {
	payload, err := marshalMessage(message)
	if err != nil {
		return false
	}
	for _, session := range sessions {
		if session != nil {
			session.enqueueSnapshot(payload)
		}
	}
	return true
}

func enqueueControlMessage(session *clientSession, message any) bool {
	payload, err := marshalMessage(message)
	if err != nil {
		return false
	}
	return session.enqueueControl(payload)
}

func (r *room) hasPreStartMatch() bool {
	return r.Status != RoomStatusStarted && r.matchStatus != ""
}

func (r *room) allMatchClientsAttached(matchPlayerCount int) bool {
	if len(r.Players) < matchPlayerCount {
		return false
	}
	for _, player := range r.Players {
		session, ok := r.clients[player.ID]
		if !ok || session == nil || session.conn == nil {
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
