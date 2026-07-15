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
	CloseNow() error
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

type terminalWriterCommand struct {
	snapshot []byte
	gameEnd  []byte
	reason   string
}

type clientLifecyclePublication struct {
	ready   bool
	publish func()
	done    chan struct{}
}

type clientSession struct {
	conn                clientConn
	snapshots           chan []byte
	control             chan writerCommand
	terminalHandoff     chan terminalWriterCommand
	done                chan struct{}
	closeDone           chan struct{}
	transportCloseStart chan struct{}
	writerDone          chan struct{}
	heartbeatDone       chan struct{}
	writerCtx           context.Context
	cancelWriter        context.CancelFunc
	enqueueMu           sync.Mutex
	heartbeatMu         sync.Mutex
	ioErrorMu           sync.Mutex
	publicationMu       sync.Mutex
	closeOnce           sync.Once
	forceOnce           sync.Once
	heartbeatDoneOnce   sync.Once
	onClose             func(*clientSession)
	publications        []*clientLifecyclePublication
	ioErrorCategory     string
	ioErrorStatus       string
	publicationDraining bool
	heartbeatStarted    bool
	terminal            bool
}

func newClientSession(conn clientConn, onClose func(*clientSession)) *clientSession {
	writerCtx, cancelWriter := context.WithCancel(context.Background())
	session := &clientSession{
		conn:                conn,
		snapshots:           make(chan []byte, 1),
		control:             make(chan writerCommand, 8),
		terminalHandoff:     make(chan terminalWriterCommand, 1),
		done:                make(chan struct{}),
		closeDone:           make(chan struct{}),
		transportCloseStart: make(chan struct{}),
		writerDone:          make(chan struct{}),
		heartbeatDone:       make(chan struct{}),
		writerCtx:           writerCtx,
		cancelWriter:        cancelWriter,
		onClose:             onClose,
	}
	go session.writeLoop()
	return session
}

// Ready lifecycle publications are synchronous: the mutator returns only after
// its log and Observer callbacks complete, including when another goroutine owns
// the FIFO drainer. Those callbacks are bounded pure sinks and must not call
// Store methods or reenter client lifecycle publication.
func (s *clientSession) enqueueLifecyclePublication(ready bool, publish func()) *clientLifecyclePublication {
	publication := &clientLifecyclePublication{
		ready:   ready,
		publish: publish,
		done:    make(chan struct{}),
	}
	s.publicationMu.Lock()
	s.publications = append(s.publications, publication)
	shouldDrain := ready && s.startPublicationDrainLocked()
	s.publicationMu.Unlock()
	// A not-ready connected publication is prepared while room.mu is held. It
	// must return without running callbacks or waiting for publication.
	if !ready {
		return publication
	}
	if shouldDrain {
		s.drainLifecyclePublications()
	} else {
		<-publication.done
	}
	return publication
}

func (s *clientSession) readyLifecyclePublication(publication *clientLifecyclePublication) {
	if publication == nil {
		return
	}
	s.publicationMu.Lock()
	publication.ready = true
	shouldDrain := s.startPublicationDrainLocked()
	s.publicationMu.Unlock()
	if shouldDrain {
		s.drainLifecyclePublications()
	} else {
		<-publication.done
	}
}

func (s *clientSession) startPublicationDrainLocked() bool {
	if s.publicationDraining || len(s.publications) == 0 || !s.publications[0].ready {
		return false
	}
	s.publicationDraining = true
	return true
}

func (s *clientSession) drainLifecyclePublications() {
	var firstPanic any
	hasPanic := false
	for {
		s.publicationMu.Lock()
		if len(s.publications) == 0 || !s.publications[0].ready {
			s.publicationDraining = false
			s.publicationMu.Unlock()
			if hasPanic {
				panic(firstPanic)
			}
			return
		}
		publication := s.publications[0]
		if len(s.publications) == 1 {
			s.publications = nil
		} else {
			s.publications = s.publications[1:]
		}
		s.publicationMu.Unlock()
		panicValue, panicked := captureCallbackPanic(publication.publish)
		close(publication.done)
		if panicked && !hasPanic {
			firstPanic = panicValue
			hasPanic = true
		}
	}
}

func (s *clientSession) ioError() (string, string) {
	s.ioErrorMu.Lock()
	defer s.ioErrorMu.Unlock()
	return s.ioErrorCategory, s.ioErrorStatus
}

func (s *clientSession) startHeartbeat(clock clock, interval time.Duration, timeout time.Duration) {
	s.heartbeatMu.Lock()
	if s.conn == nil || s.isDone() {
		s.heartbeatDoneOnce.Do(func() { close(s.heartbeatDone) })
		s.heartbeatMu.Unlock()
		return
	}
	ticker := clock.NewTicker(interval)
	s.heartbeatStarted = true
	go func() {
		defer s.heartbeatDoneOnce.Do(func() { close(s.heartbeatDone) })
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C():
				ctx, cancel := context.WithTimeout(s.writerCtx, timeout)
				err := s.conn.Ping(ctx)
				timedOut := errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
				cancel()
				if err != nil {
					category := "ping_failed"
					if timedOut {
						category = "ping_timeout"
					}
					s.closeWithIOError(websocket.StatusGoingAway, "heartbeat failed", category, "")
					return
				}
			case <-s.done:
				return
			}
		}
	}()
	s.heartbeatMu.Unlock()
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
	s.terminalHandoff <- terminalWriterCommand{snapshot: snapshot, gameEnd: gameEnd, reason: reason}
	s.enqueueMu.Unlock()
	return true
}

func (s *clientSession) writeLoop() {
	defer close(s.writerDone)
	for {
		if s.isDone() {
			return
		}
		select {
		case command := <-s.control:
			if !s.writeCommand(command) {
				return
			}
			continue
		default:
		}
		select {
		case terminal := <-s.terminalHandoff:
			s.writeTerminal(terminal)
			return
		default:
		}

		select {
		case command := <-s.control:
			if !s.writeCommand(command) {
				return
			}
		case terminal := <-s.terminalHandoff:
			s.writeTerminal(terminal)
			return
		case payload := <-s.snapshots:
			if !s.writeCommand(writerCommand{kind: writerCommandPayload, payload: payload}) {
				return
			}
		case <-s.done:
			return
		}
	}
}

func (s *clientSession) writeTerminal(terminal terminalWriterCommand) {
	for {
		select {
		case command := <-s.control:
			if !s.writeCommand(command) {
				return
			}
		default:
			for _, command := range [...]writerCommand{
				{kind: writerCommandPayload, payload: terminal.snapshot},
				{kind: writerCommandPayload, payload: terminal.gameEnd},
				{kind: writerCommandClose, reason: terminal.reason},
			} {
				if !s.writeCommand(command) {
					return
				}
			}
			return
		}
	}
}

func (s *clientSession) writeCommand(command writerCommand) bool {
	if s.isDone() {
		return false
	}
	if command.kind == writerCommandClose {
		s.close(websocket.StatusNormalClosure, command.reason)
		return false
	}
	ctx, cancel := context.WithTimeout(s.writerCtx, webSocketWriteTimeout)
	err := s.conn.Write(ctx, websocket.MessageText, command.payload)
	cancel()
	if err != nil {
		s.closeWithIOError(websocket.StatusGoingAway, "write failed", "write_failed", "")
		return false
	}
	return true
}

func (s *clientSession) isDone() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *clientSession) close(code websocket.StatusCode, reason string) {
	s.closeWithCause(code, reason, "", "")
}

func (s *clientSession) closeWithIOError(code websocket.StatusCode, reason string, category string, status string) {
	s.closeWithCause(code, reason, category, status)
}

func (s *clientSession) closeWithCause(code websocket.StatusCode, reason string, category string, status string) {
	s.closeOnce.Do(func() {
		defer close(s.closeDone)
		if category != "" {
			s.ioErrorMu.Lock()
			s.ioErrorCategory = category
			s.ioErrorStatus = status
			s.ioErrorMu.Unlock()
		}
		close(s.done)
		s.cancelWriter()
		s.heartbeatMu.Lock()
		if !s.heartbeatStarted {
			s.heartbeatDoneOnce.Do(func() { close(s.heartbeatDone) })
		}
		s.heartbeatMu.Unlock()
		if s.onClose != nil {
			s.onClose(s)
		}
		close(s.transportCloseStart)
		if s.conn != nil {
			_ = s.conn.Close(code, reason)
		}
	})
}

// forceClose starts the logical close if necessary, then interrupts a transport
// that may already be blocked inside another closeOnce owner.
func (s *clientSession) forceClose(code websocket.StatusCode, reason string) {
	go s.close(code, reason)
	select {
	case <-s.transportCloseStart:
	case <-s.closeDone:
	}
	s.forceOnce.Do(func() {
		if s.conn != nil {
			_ = s.conn.CloseNow()
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
			s.logWebSocketEvent("websocket_auth_rejected", roomID, playerID, "category", "invalid_token")
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
			recordWebSocketReadError(session, err)
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

func recordWebSocketReadError(session *clientSession, err error) {
	status := websocket.CloseStatus(err)
	if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
		return
	}
	if status == -1 {
		session.closeWithIOError(websocket.StatusNormalClosure, "", "read_failed", "")
		return
	}
	session.closeWithIOError(websocket.StatusNormalClosure, "", "read_close", normalizedWebSocketStatus(status))
}

func normalizedWebSocketStatus(status websocket.StatusCode) string {
	switch status {
	case websocket.StatusPolicyViolation:
		return "policy_violation"
	case websocket.StatusUnsupportedData:
		return "unsupported_data"
	case websocket.StatusInvalidFramePayloadData:
		return "invalid_payload"
	case websocket.StatusMessageTooBig:
		return "message_too_big"
	case websocket.StatusInternalError:
		return "internal_error"
	case websocket.StatusAbnormalClosure:
		return "abnormal_closure"
	default:
		return "other"
	}
}

func (s *Store) reserveClient(roomID string, playerID string, tokens []string) (*clientReservation, error) {
	if !s.beginMutation() {
		return nil, ErrRoomNotFound
	}
	defer s.endMutation()

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

	if !s.beginMutation() {
		return nil, false
	}
	defer s.endMutation()

	if reservation == nil || reservation.room == nil {
		return nil, false
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, false
	}
	room := reservation.room
	room.mu.Lock()
	if room.removed || room.clients == nil || room.reservations == nil || room.reservations[reservation.playerID] != reservation {
		room.mu.Unlock()
		s.mu.Unlock()
		return nil, false
	}
	session := newClientSession(conn, func(expected *clientSession) {
		s.releaseClient(reservation, expected)
	})
	connectedClient := clientObservation{roomID: room.ID, playerID: reservation.playerID, session: session}
	connectedTransition := s.observation.connectedClientsDelta(1)
	connectedPublication := s.prepareConnectedClientPublication(connectedClient, connectedTransition)
	delete(room.reservations, reservation.playerID)
	room.clients[reservation.playerID] = session
	lifecycleDone := make(chan struct{})
	s.activeSessions[session] = lifecycleDone
	s.monitorClientSession(session, lifecycleDone)
	session.startHeartbeat(s.clock, s.heartbeatInterval, s.heartbeatTimeout)
	s.mu.Unlock()
	room.lastActivityAt = s.clock.Now()
	room.disconnectedAt = time.Time{}
	matchCapacity := room.gameConfig.MatchPlayerCount()
	if room.hasPreStartMatch() && room.matchStatus == MatchStatusMatched && room.allMatchClientsAttached(matchCapacity) {
		room.matchStatus = MatchStatusLoading
		deliveries = append(deliveries, room.readyEventDeliveries()...)
		if room.allMatchPlayersReady(matchCapacity) {
			s.startMatchCountdownLocked(room)
			deliveries = append(deliveries, room.matchSnapshotDeliveries(MatchStatusStarting, room.countdown)...)
		}
	}
	failedSessions := tryEnqueueWebSocketDeliveries(deliveries)
	room.mu.Unlock()

	session.readyLifecyclePublication(connectedPublication)
	closeClientSessions(failedSessions, "control delivery failed")
	return session, true
}

func (s *Store) monitorClientSession(session *clientSession, lifecycleDone chan struct{}) {
	go func() {
		<-session.closeDone
		<-session.writerDone
		<-session.heartbeatDone

		s.mu.Lock()
		if s.activeSessions[session] == lifecycleDone {
			delete(s.activeSessions, session)
		}
		s.mu.Unlock()
		close(lifecycleDone)
	}()
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
	clientTransitions := s.clientObservationTransitionsLocked([]clientObservation{{
		roomID:   room.ID,
		playerID: playerID,
		session:  currentSession,
	}}, -1)
	var playerIDs []string
	if room.hasPreStartMatch() {
		clientStart := len(resources.clientObservations)
		playerIDs, shouldClose = resources.removeRoomLocked(room)
		clientTransitions = append(clientTransitions,
			s.clientObservationTransitionsLocked(resources.clientObservations[clientStart:], -1)...)
	}
	if room.Status == RoomStatusStarted && len(room.clients) == 0 {
		room.disconnectedAt = s.clock.Now()
	}
	room.mu.Unlock()
	s.publishDisconnectedClients(clientTransitions)

	if shouldClose {
		if s.deleteRoomIfSame(room.ID, room) {
			s.releasePlayerIDs(playerIDs)
		}
		resources.close(defaultMatchCancelMsg)
	}
}

func (s *Store) setInput(roomID string, playerID string, input inputMessage, expectedSession *clientSession) {
	if !s.beginMutation() {
		return
	}
	defer s.endMutation()

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

	if !s.beginMutation() {
		return
	}
	defer s.endMutation()

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
	if room.matchStatus == MatchStatusLoading && room.allMatchPlayersReady(room.gameConfig.MatchPlayerCount()) {
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
	countdownTicker := s.clock.NewTicker(time.Second)
	countdownStop := make(chan struct{})
	room.countdownTicker = countdownTicker
	room.countdownStop = countdownStop
	if !s.launchRoomWorker(func() { s.runMatchCountdown(room, countdownTicker, countdownStop) }) {
		countdownTicker.Stop()
		close(countdownStop)
		room.countdownTicker = nil
		room.countdownStop = nil
	}
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
	started := s.startRoomLocked(room)
	deliveries = append(deliveries, room.matchSnapshotDeliveries(MatchStatusStarted, 0)...)
	failedSessions := tryEnqueueWebSocketDeliveries(deliveries)
	room.mu.Unlock()

	if started {
		s.logRoomEvent("room_started", room.ID)
	}
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
	var snapshotMarshalErr error
	var clientTransitions []clientObservationTransition
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
	stepStarted := s.wallNow()
	snapshot := room.state.Step(inputs)
	stepDuration := s.wallNow().Sub(stepStarted)
	room.latestSnapshot = snapshotSummaryFromSnapshot(snapshot)
	message := roomSnapshotMessage{Type: "snapshot", Snapshot: roomSnapshotFromSimulation(snapshot, MatchStatusStarted)}

	for _, session := range room.clients {
		if session != nil {
			snapshotSessions = append(snapshotSessions, session)
		}
	}
	results := calculateGameEndResults(room.gameConfig, snapshot)
	var playerIDs []string
	if len(results) > 0 {
		snapshotPayload, snapshotMarshalErr = marshalMessage(message)
		if snapshotMarshalErr == nil {
			deliveries = append(deliveries, room.gameEndDeliveries(results)...)
		}
		for playerID, session := range room.clients {
			if session != nil {
				resources.clientObservations = append(resources.clientObservations, clientObservation{
					roomID: room.ID, playerID: playerID, session: session,
				})
			}
		}
		clientTransitions = s.clientObservationTransitionsLocked(resources.clientObservations, -1)
		room.clients = nil
		playerIDs, gameEnded = resources.removeRoomLocked(room)
	} else {
		enqueueSnapshotMessage(snapshotSessions, message)
	}
	room.mu.Unlock()
	s.observation.observeTick(stepDuration)
	s.publishDisconnectedClients(clientTransitions)

	if gameEnded {
		if s.deleteRoomIfSame(room.ID, room) {
			s.releasePlayerIDs(playerIDs)
			s.logRoomEvent("room_ended", room.ID)
		}
	}
	if gameEnded {
		if snapshotMarshalErr != nil {
			closeClientSessions(snapshotSessions, "message marshal failed")
			resources.close(defaultGameEndCloseMsg)
			return
		}
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
