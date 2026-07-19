package rooms

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

// Store owns registry and store-lifecycle synchronization only.
//
// mutationMu is the outer quiescing gate for externally initiated mutations.
// mu protects rooms, activeSessions, playerIDs, random, and closed. Room
// gameplay, connection, countdown, and resource fields are protected by room.mu
// instead. The lock order is mutationMu -> Store.mu -> room.mu; acquiring an
// outer lock while holding an inner lock is forbidden. matchmakingMu serializes
// one complete waiting-room find-or-create transition, with lock order
// mutationMu -> matchmakingMu -> Store.mu -> room.mu. workerMu is a leaf lock
// that closes the gameplay/countdown launch gate before shutdown waits on
// workerWG; no core lock is acquired while workerMu is held.
type Store struct {
	mutationMu        sync.RWMutex
	matchmakingMu     sync.Mutex
	mu                sync.RWMutex
	shutdownOnce      sync.Once
	shutdownErrMu     sync.Mutex
	workerMu          sync.Mutex
	workerWG          sync.WaitGroup
	maxActiveRooms    int
	rooms             map[string]*room
	activeSessions    map[*clientSession]chan struct{}
	playerIDs         map[string]struct{}
	random            io.Reader
	clock             clock
	wallNow           func() time.Time
	gameMap           simulation.MapData
	gameConfig        simulation.GameConfig
	logger            *slog.Logger
	observation       *observationState
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	janitorStop       chan struct{}
	janitorDone       chan struct{}
	shutdownDone      chan struct{}
	shutdownErr       error
	shutdownPanic     any
	shutdownPanicked  bool
	workersClosing    bool
	closed            bool
}

type StoreConfig struct {
	Map               simulation.MapData
	GameConfig        simulation.GameConfig
	Random            io.Reader
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	// Logger and Observer handlers run synchronously as bounded pure sinks after
	// core locks are released. They must not call Store methods. Lifecycle
	// mutators wait for their log and Observer publication before returning.
	Logger   *slog.Logger
	Observer Observer
}

// room owns synchronization for one room independently of the Store registry.
// ID, gameConfig, and calculateGameEnd are immutable. mu protects every other
// field, including removed and all gameplay, client, countdown, and resource state.
type room struct {
	ID               string
	gameConfig       simulation.GameConfig
	calculateGameEnd gameEndCalculator
	mu               sync.Mutex

	removed                  bool
	ending                   bool
	Status                   RoomStatus
	Players                  []playerResponse
	matchStatus              MatchStatus
	readyPlayers             map[string]bool
	sessions                 map[string]playerSession
	countdown                int
	state                    simulationStepper
	pendingInputs            map[string]simulation.InputCommand
	clients                  map[string]*clientSession
	closeBarrierSessions     map[*clientSession]struct{}
	reservations             map[string]*clientReservation
	finalizedGameEndResults  map[string]gameEndResult
	finalizedGameEndSessions map[string]*clientSession
	gameEndCleanupDone       chan struct{}
	gameEndCleanupOnce       sync.Once
	gameEndCleanupWorkerDone chan struct{}
	gameEndCleanupWorkerOnce sync.Once
	latestSnapshot           snapshotSummary
	createdAt                time.Time
	lastActivityAt           time.Time
	disconnectedAt           time.Time
	ticker                   ticker
	stop                     chan struct{}
	countdownTicker          ticker
	countdownStop            chan struct{}
}

type simulationStepper interface {
	Step([]simulation.InputCommand) simulation.Snapshot
}

func NewStore(maxActiveRooms int) *Store {
	return newStore(maxActiveRooms, nil, StoreConfig{})
}

func NewStoreWithClock(maxActiveRooms int, clock clock) *Store {
	return newStore(maxActiveRooms, clock, StoreConfig{})
}

func NewStoreWithConfig(maxActiveRooms int, config StoreConfig) *Store {
	return newStore(maxActiveRooms, nil, config)
}

func newStore(maxActiveRooms int, clock clock, config StoreConfig) *Store {
	if maxActiveRooms <= 0 {
		maxActiveRooms = 5
	}
	if clock == nil {
		clock = realClock{}
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	gameConfig := config.GameConfig
	if gameConfig.Version <= 0 {
		gameConfig = simulation.StaticGameConfig()
	}
	if config.Map.Width > 0 || config.Map.Height > 0 || len(config.Map.Map) > 0 {
		gameConfig.Map = config.Map
	}
	resolvedConfig, err := simulation.ResolveGameConfig(gameConfig)
	if err != nil {
		resolvedConfig = simulation.StaticGameConfig()
	}
	heartbeatInterval := config.HeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = defaultHeartbeatInterval
	}
	heartbeatTimeout := config.HeartbeatTimeout
	if heartbeatTimeout <= 0 {
		heartbeatTimeout = defaultHeartbeatTimeout
	}

	store := &Store{
		maxActiveRooms:    maxActiveRooms,
		rooms:             make(map[string]*room),
		activeSessions:    make(map[*clientSession]chan struct{}),
		playerIDs:         make(map[string]struct{}),
		random:            random,
		clock:             clock,
		wallNow:           time.Now,
		gameMap:           resolvedConfig.Map,
		gameConfig:        resolvedConfig,
		logger:            normalizeLogger(config.Logger),
		observation:       newObservationState(config.Observer),
		heartbeatInterval: heartbeatInterval,
		heartbeatTimeout:  heartbeatTimeout,
		janitorStop:       make(chan struct{}),
		janitorDone:       make(chan struct{}),
		shutdownDone:      make(chan struct{}),
	}
	store.startJanitor()
	return store
}

// beginMutation holds the shared quiescing gate for the entire externally
// initiated mutation, including synchronous log and metrics publication.
func (s *Store) beginMutation() bool {
	s.mutationMu.RLock()
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		s.mutationMu.RUnlock()
		return false
	}
	return true
}

func (s *Store) endMutation() {
	s.mutationMu.RUnlock()
}

func (s *Store) launchRoomWorker(worker func()) bool {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.workersClosing {
		return false
	}
	s.workerWG.Add(1)
	go func() {
		defer s.workerWG.Done()
		worker()
	}()
	return true
}

func (s *Store) stopLaunchingRoomWorkers() {
	s.workerMu.Lock()
	s.workersClosing = true
	s.workerMu.Unlock()
}

func normalizeLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return logger
}

func (s *Store) listRooms() roomListResponse {
	registered := s.registeredRooms()
	rooms := make([]roomResponse, 0, len(registered))
	for _, room := range registered {
		room.mu.Lock()
		if !room.removed {
			rooms = append(rooms, room.toResponse(s.gameMap))
		}
		room.mu.Unlock()
	}
	return roomListResponse{Rooms: rooms}
}

func (s *Store) createRoom() (roomResponse, error) {
	if !s.beginMutation() {
		return roomResponse{}, ErrInternal
	}
	defer s.endMutation()

	response, err := s.createRoomOnce()
	if !errors.Is(err, ErrActiveRoomCapReached) {
		return response, err
	}

	s.cleanupExpired(s.clock.Now())
	return s.createRoomOnce()
}

func (s *Store) createRoomOnce() (roomResponse, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return roomResponse{}, ErrInternal
	}
	if len(s.rooms) >= s.maxActiveRooms {
		s.mu.Unlock()
		return roomResponse{}, ErrActiveRoomCapReached
	}

	roomID, err := s.uniqueRoomIDLocked()
	if err != nil {
		s.mu.Unlock()
		return roomResponse{}, err
	}
	room := s.newRoomLocked(roomID, s.gameConfig)
	response := room.toResponse(s.gameMap)
	s.rooms[room.ID] = room
	transition := s.observation.activeRoomsDelta(1)
	s.mu.Unlock()

	s.observation.publish(transition)
	s.logRoomEvent("room_created", room.ID)
	return response, nil
}

func (s *Store) clearRooms() clearRoomsResponse {
	if !s.beginMutation() {
		return clearRoomsResponse{}
	}
	defer s.endMutation()

	registered := s.registeredRooms()
	deleted := 0
	var resources roomResources
	for _, room := range registered {
		clientStart := len(resources.clientObservations)
		room.mu.Lock()
		if room.ending {
			room.mu.Unlock()
			continue
		}
		playerIDs, removed := resources.removeRoomLocked(room)
		clientTransitions := s.clientObservationTransitionsLocked(resources.clientObservations[clientStart:], -1)
		room.mu.Unlock()
		s.publishDisconnectedClients(clientTransitions)
		if removed && s.deleteRoomIfSame(room.ID, room) {
			s.releasePlayerIDs(playerIDs)
			deleted++
		}
	}

	resources.close(defaultRoomDebugDeleteMsg)
	return clearRoomsResponse{Deleted: deleted}
}

func (s *Store) deleteRoom(roomID string) (clearRoomsResponse, bool) {
	if !s.beginMutation() {
		return clearRoomsResponse{}, false
	}
	defer s.endMutation()

	room := s.lookupRoom(roomID)
	if room == nil {
		return clearRoomsResponse{}, false
	}

	var resources roomResources
	room.mu.Lock()
	if room.ending {
		room.mu.Unlock()
		return clearRoomsResponse{}, false
	}
	playerIDs, removed := resources.removeRoomLocked(room)
	clientTransitions := s.clientObservationTransitionsLocked(resources.clientObservations, -1)
	room.mu.Unlock()
	s.publishDisconnectedClients(clientTransitions)
	if !removed || !s.deleteRoomIfSame(roomID, room) {
		resources.close(defaultRoomDebugDeleteMsg)
		return clearRoomsResponse{}, false
	}

	s.releasePlayerIDs(playerIDs)
	resources.close(defaultRoomDebugDeleteMsg)
	return clearRoomsResponse{Deleted: 1}, true
}

func (s *Store) lookupRoom(roomID string) *room {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rooms[roomID]
}

func (s *Store) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

func (s *Store) registeredRooms() []*room {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rooms := make([]*room, 0, len(s.rooms))
	for _, room := range s.rooms {
		rooms = append(rooms, room)
	}
	return rooms
}

func (s *Store) deleteRoomIfSame(roomID string, expected *room) bool {
	transition, deleted := s.removeRegisteredRoomIfSame(roomID, expected)
	if !deleted {
		return false
	}
	s.observation.publish(transition)
	return true
}

func (s *Store) removeRegisteredRoomIfSame(roomID string, expected *room) (observationTransition, bool) {
	s.mu.Lock()
	if expected == nil || s.rooms[roomID] != expected {
		s.mu.Unlock()
		return observationTransition{}, false
	}
	delete(s.rooms, roomID)
	transition := s.observation.activeRoomsDelta(-1)
	s.mu.Unlock()
	return transition, true
}

type clientObservationTransition struct {
	clientObservation
	transition observationTransition
}

func (s *Store) clientObservationTransitionsLocked(clients []clientObservation, delta int) []clientObservationTransition {
	transitions := make([]clientObservationTransition, 0, len(clients))
	for _, client := range clients {
		if client.session == nil {
			continue
		}
		transitions = append(transitions, clientObservationTransition{
			clientObservation: client,
			transition:        s.observation.connectedClientsDelta(delta),
		})
	}
	return transitions
}

func (s *Store) prepareConnectedClientPublication(client clientObservation, transition observationTransition) *clientLifecyclePublication {
	return client.session.enqueueLifecyclePublication(false, func() {
		s.observation.publish(transition)
		s.logWebSocketEvent("websocket_connected", client.roomID, client.playerID)
	})
}

func (s *Store) publishDisconnectedClients(transitions []clientObservationTransition) {
	for _, observed := range transitions {
		observed.session.enqueueLifecyclePublication(true, func() {
			s.observation.publish(observed.transition)
			category, status := observed.session.ioError()
			if category != "" {
				attrs := []any{"category", category}
				if status != "" {
					attrs = append(attrs, "status", status)
				}
				s.logWebSocketEvent("websocket_io_error", observed.roomID, observed.playerID, attrs...)
			}
			s.logWebSocketEvent("websocket_disconnected", observed.roomID, observed.playerID)
		})
	}
}

func (s *Store) getRoomResponse(roomID string) (roomResponse, bool) {
	room := s.lookupRoom(roomID)
	if room == nil {
		return roomResponse{}, false
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.removed {
		return roomResponse{}, false
	}
	return room.toResponse(s.gameMap), true
}

func (s *Store) addPlayer(roomID string) (playerSessionResponse, error) {
	if !s.beginMutation() {
		return playerSessionResponse{}, ErrInternal
	}
	defer s.endMutation()

	room := s.lookupRoom(roomID)
	if room == nil {
		return playerSessionResponse{}, ErrRoomNotFound
	}

	credentials, err := s.issuePlayerCredentials()
	if err != nil {
		return playerSessionResponse{}, err
	}

	room.mu.Lock()
	if room.removed || room.ending {
		room.mu.Unlock()
		s.releasePlayerID(credentials.id)
		return playerSessionResponse{}, ErrRoomNotFound
	}
	if room.matchStatus != "" || len(room.Players) >= s.debugRoomCapacity() {
		room.mu.Unlock()
		s.releasePlayerID(credentials.id)
		return playerSessionResponse{}, ErrRoomFull
	}
	issued := s.addPlayerLocked(room, credentials)
	room.mu.Unlock()
	return issued, nil
}

func (s *Store) joinMatchmaking(gameMode string) (matchmakingJoinResponse, error) {
	if !s.beginMutation() {
		return matchmakingJoinResponse{}, ErrInternal
	}
	defer s.endMutation()
	s.matchmakingMu.Lock()
	defer s.matchmakingMu.Unlock()

	selectedConfig, err := s.gameConfig.SelectMode(gameMode)
	if err != nil {
		return matchmakingJoinResponse{}, ErrInvalidGameMode
	}

	var credentials *playerCredentials
	for _, room := range s.registeredRooms() {
		room.mu.Lock()
		canJoin := room.gameConfig.SelectedMode.ID == selectedConfig.SelectedMode.ID &&
			room.canAcceptMatchmakingLocked(s.debugRoomCapacity())
		room.mu.Unlock()
		if !canJoin {
			continue
		}
		if credentials == nil {
			issued, err := s.issuePlayerCredentials()
			if err != nil {
				return matchmakingJoinResponse{}, err
			}
			credentials = &issued
		}
		if joined, ok := s.tryJoinMatchmakingRoom(room, *credentials); ok {
			return joined, nil
		}
	}

	response, err := s.createMatchmakingRoom(credentials, selectedConfig)
	if errors.Is(err, ErrActiveRoomCapReached) {
		s.cleanupExpired(s.clock.Now())
		response, err = s.createMatchmakingRoom(credentials, selectedConfig)
	}
	if err != nil && credentials != nil {
		s.releasePlayerID(credentials.id)
	}
	return response, err
}

func (s *Store) tryJoinMatchmakingRoom(room *room, credentials playerCredentials) (matchmakingJoinResponse, bool) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if !room.canAcceptMatchmakingLocked(s.debugRoomCapacity()) {
		return matchmakingJoinResponse{}, false
	}

	issued := s.addPlayerLocked(room, credentials)
	s.markRoomMatchedIfFullLocked(room)
	return matchmakingJoinResponseFrom(room.toResponse(s.gameMap), issued), true
}

func (s *Store) createMatchmakingRoom(credentials *playerCredentials, gameConfig simulation.GameConfig) (matchmakingJoinResponse, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return matchmakingJoinResponse{}, ErrInternal
	}
	if len(s.rooms) >= s.maxActiveRooms {
		s.mu.Unlock()
		return matchmakingJoinResponse{}, ErrActiveRoomCapReached
	}
	roomID, err := s.uniqueRoomIDLocked()
	if err != nil {
		s.mu.Unlock()
		return matchmakingJoinResponse{}, err
	}
	if credentials == nil {
		issued, err := s.issuePlayerCredentialsLocked()
		if err != nil {
			s.mu.Unlock()
			return matchmakingJoinResponse{}, err
		}
		credentials = &issued
	}

	room := s.newRoomLocked(roomID, gameConfig)
	issued := s.addPlayerLocked(room, *credentials)
	s.markRoomMatchedIfFullLocked(room)
	response := matchmakingJoinResponseFrom(room.toResponse(s.gameMap), issued)
	s.rooms[room.ID] = room
	transition := s.observation.activeRoomsDelta(1)
	s.mu.Unlock()

	s.observation.publish(transition)
	s.logRoomEvent("room_created", room.ID)
	return response, nil
}

func (s *Store) markRoomMatchedIfFullLocked(room *room) {
	if len(room.Players) != room.gameConfig.MatchPlayerCount() {
		return
	}
	room.matchStatus = MatchStatusMatched
	room.readyPlayers = make(map[string]bool)
	room.lastActivityAt = s.clock.Now()
}

func matchmakingJoinResponseFrom(room roomResponse, issued playerSessionResponse) matchmakingJoinResponse {
	return matchmakingJoinResponse{
		GameMode:      room.GameMode,
		Room:          room,
		Player:        issued.Player,
		SessionToken:  issued.SessionToken,
		WebSocketPath: issued.WebSocketPath,
	}
}

func (r *room) canAcceptMatchmakingLocked(debugCapacity int) bool {
	return !r.removed && !r.ending &&
		r.Status == RoomStatusWaiting &&
		r.matchStatus == "" &&
		len(r.Players) < debugCapacity &&
		len(r.Players) < r.gameConfig.MatchPlayerCount()
}

func (s *Store) debugRoomCapacity() int {
	return s.gameMap.MaxPlayers
}

func (s *Store) matchCapacity() int {
	return s.gameConfig.MatchPlayerCount()
}

func (s *Store) defaultGameMode() string {
	return s.gameConfig.ModeCatalog.Default
}

func (s *Store) startRoom(roomID string) (roomResponse, error) {
	if !s.beginMutation() {
		return roomResponse{}, ErrInternal
	}
	defer s.endMutation()

	room := s.lookupRoom(roomID)
	if room == nil {
		return roomResponse{}, ErrRoomNotFound
	}
	room.mu.Lock()
	if room.removed || room.ending {
		room.mu.Unlock()
		return roomResponse{}, ErrRoomNotFound
	}
	if len(room.Players) == 0 {
		room.mu.Unlock()
		return roomResponse{}, ErrRoomHasNoPlayers
	}

	started := s.startRoomLocked(room)
	response := room.toResponse(s.gameMap)
	room.mu.Unlock()
	if started {
		s.logRoomEvent("room_started", room.ID)
	}
	return response, nil
}

func (s *Store) logRoomEvent(event string, roomID string) {
	switch event {
	case "room_created", "room_started", "room_ended", "room_expired":
	default:
		return
	}
	s.logger.Info(event, "event", event, "roomID", roomID)
}

func (s *Store) logWebSocketEvent(event string, roomID string, playerID string, attrs ...any) {
	switch event {
	case "websocket_connected", "websocket_disconnected":
		attrs = nil
	case "websocket_auth_rejected":
		attrs = boundedWebSocketLogAttrs(attrs, map[string]bool{"category": true}, map[string]bool{"invalid_token": true}, nil)
	case "websocket_io_error":
		attrs = boundedWebSocketLogAttrs(attrs,
			map[string]bool{"category": true, "status": true},
			map[string]bool{
				"read_failed": true, "write_failed": true, "ping_failed": true,
				"ping_timeout": true, "read_close": true,
			},
			map[string]bool{
				"policy_violation": true, "unsupported_data": true, "invalid_payload": true,
				"message_too_big": true, "internal_error": true, "abnormal_closure": true,
				"other": true,
			},
		)
	default:
		return
	}
	fields := []any{"event", event, "roomID", roomID, "playerID", playerID}
	fields = append(fields, attrs...)
	s.logger.Info(event, fields...)
}

func boundedWebSocketLogAttrs(attrs []any, allowedKeys map[string]bool, allowedCategories map[string]bool, allowedStatuses map[string]bool) []any {
	bounded := make([]any, 0, 4)
	for index := 0; index+1 < len(attrs); index += 2 {
		key, keyOK := attrs[index].(string)
		value, valueOK := attrs[index+1].(string)
		if !keyOK || !valueOK || !allowedKeys[key] {
			continue
		}
		if key == "category" && !allowedCategories[value] {
			continue
		}
		if key == "status" && !allowedStatuses[value] {
			continue
		}
		bounded = append(bounded, key, value)
	}
	return bounded
}

func (s *Store) newRoomLocked(roomID string, gameConfig simulation.GameConfig) *room {
	now := s.clock.Now()
	room := &room{
		ID:                       roomID,
		gameConfig:               gameConfig,
		calculateGameEnd:         calculateGameEndResults,
		Status:                   RoomStatusWaiting,
		sessions:                 make(map[string]playerSession),
		pendingInputs:            make(map[string]simulation.InputCommand),
		clients:                  make(map[string]*clientSession),
		closeBarrierSessions:     make(map[*clientSession]struct{}),
		reservations:             make(map[string]*clientReservation),
		finalizedGameEndResults:  make(map[string]gameEndResult),
		finalizedGameEndSessions: make(map[string]*clientSession),
		gameEndCleanupDone:       make(chan struct{}),
		gameEndCleanupWorkerDone: make(chan struct{}),
		createdAt:                now,
		lastActivityAt:           now,
	}
	return room
}

type playerCredentials struct {
	id           string
	sessionToken string
	session      playerSession
}

func (s *Store) issuePlayerCredentials() (playerCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return playerCredentials{}, ErrInternal
	}
	return s.issuePlayerCredentialsLocked()
}

func (s *Store) issuePlayerCredentialsLocked() (playerCredentials, error) {
	playerID, err := s.uniquePlayerIDLocked()
	if err != nil {
		return playerCredentials{}, err
	}
	sessionToken, err := randomValue(s.random, "", sessionRandomBytes)
	if err != nil {
		return playerCredentials{}, ErrInternal
	}
	s.playerIDs[playerID] = struct{}{}
	return playerCredentials{
		id:           playerID,
		sessionToken: sessionToken,
		session:      playerSession{digest: sha256.Sum256([]byte(sessionToken))},
	}, nil
}

func (s *Store) releasePlayerID(playerID string) {
	s.releasePlayerIDs([]string{playerID})
}

func (s *Store) releasePlayerIDs(playerIDs []string) {
	if len(playerIDs) == 0 {
		return
	}
	s.mu.Lock()
	for _, playerID := range playerIDs {
		delete(s.playerIDs, playerID)
	}
	s.mu.Unlock()
}

func (s *Store) addPlayerLocked(room *room, credentials playerCredentials) playerSessionResponse {
	playerIndex := len(room.Players)
	team, slot, ok := room.gameConfig.TeamForPlayerIndex(playerIndex)
	if !ok {
		team = simulation.TeamRed
		slot = playerIndex
	}
	player := playerResponse{
		ID:   credentials.id,
		Team: string(team),
		Slot: slot,
	}
	issued := playerSessionResponse{
		Player:        player,
		SessionToken:  credentials.sessionToken,
		WebSocketPath: webSocketPath(room.ID, player.ID, credentials.sessionToken),
	}
	room.Players = append(room.Players, player)
	room.sessions[player.ID] = credentials.session
	room.lastActivityAt = s.clock.Now()
	return issued
}

func (s *Store) uniqueRoomIDLocked() (string, error) {
	for range identityRetryLimit {
		roomID, err := randomValue(s.random, roomIDPrefix, roomIDRandomBytes)
		if err != nil {
			return "", ErrInternal
		}
		if _, exists := s.rooms[roomID]; !exists {
			return roomID, nil
		}
	}
	return "", ErrInternal
}

func (s *Store) uniquePlayerIDLocked() (string, error) {
	for range identityRetryLimit {
		playerID, err := randomValue(s.random, playerIDPrefix, playerIDRandomBytes)
		if err != nil {
			return "", ErrInternal
		}
		if _, exists := s.playerIDs[playerID]; !exists {
			return playerID, nil
		}
	}
	return "", ErrInternal
}

func (s *Store) startRoomLocked(room *room) bool {
	started := room.Status != RoomStatusStarted
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
		room.state = simulation.NewStateWithConfig(simulationPlayers(room.Players, room.gameConfig), simulation.Config{Game: room.gameConfig})
	}
	if room.ticker == nil {
		roomTicker := s.clock.NewTicker(time.Second / time.Duration(room.gameConfig.TickRate))
		roomStop := make(chan struct{})
		room.ticker = roomTicker
		room.stop = roomStop
		if !s.launchRoomWorker(func() { s.runRoom(room, roomTicker, roomStop) }) {
			roomTicker.Stop()
			close(roomStop)
			room.ticker = nil
			room.stop = nil
		}
	}
	return started
}

// hasPlayer requires r.mu because Players is room-owned state.
func (r *room) hasPlayer(playerID string) bool {
	for _, player := range r.Players {
		if player.ID == playerID {
			return true
		}
	}
	return false
}
