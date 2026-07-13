package rooms

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

// Store owns registry and store-lifecycle synchronization only.
//
// mu protects rooms, activeSessions, playerIDs, random, and closed. Room
// gameplay, connection, countdown, and resource fields are protected by room.mu
// instead. When both locks are ever needed, Store.mu must be acquired before
// room.mu; acquiring Store.mu while holding room.mu is forbidden.
type Store struct {
	mu                sync.RWMutex
	closeOnce         sync.Once
	maxActiveRooms    int
	rooms             map[string]*room
	activeSessions    map[*clientSession]chan struct{}
	playerIDs         map[string]struct{}
	random            io.Reader
	clock             clock
	gameMap           simulation.MapData
	gameConfig        simulation.GameConfig
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	janitorStop       chan struct{}
	janitorDone       chan struct{}
	closed            bool
}

type StoreConfig struct {
	Map               simulation.MapData
	GameConfig        simulation.GameConfig
	Random            io.Reader
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
}

// room owns synchronization for one room independently of the Store registry.
// ID is immutable. mu protects every other field, including removed and all
// gameplay, client, countdown, and resource state.
type room struct {
	ID string
	mu sync.Mutex

	removed         bool
	Status          RoomStatus
	Players         []playerResponse
	matchStatus     MatchStatus
	readyPlayers    map[string]bool
	sessions        map[string]playerSession
	countdown       int
	state           simulationStepper
	pendingInputs   map[string]simulation.InputCommand
	clients         map[string]*clientSession
	reservations    map[string]*clientReservation
	latestSnapshot  snapshotSummary
	createdAt       time.Time
	lastActivityAt  time.Time
	disconnectedAt  time.Time
	ticker          ticker
	stop            chan struct{}
	countdownTicker ticker
	countdownStop   chan struct{}
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
		gameMap:           resolvedConfig.Map,
		gameConfig:        resolvedConfig,
		heartbeatInterval: heartbeatInterval,
		heartbeatTimeout:  heartbeatTimeout,
		janitorStop:       make(chan struct{}),
		janitorDone:       make(chan struct{}),
	}
	store.startJanitor()
	return store
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
	response, err := s.createRoomOnce()
	if !errors.Is(err, ErrActiveRoomCapReached) {
		return response, err
	}

	s.cleanupExpired(s.clock.Now())
	return s.createRoomOnce()
}

func (s *Store) createRoomOnce() (roomResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return roomResponse{}, ErrInternal
	}
	if len(s.rooms) >= s.maxActiveRooms {
		return roomResponse{}, ErrActiveRoomCapReached
	}

	roomID, err := s.uniqueRoomIDLocked()
	if err != nil {
		return roomResponse{}, err
	}
	room := s.newRoomLocked(roomID)
	response := room.toResponse(s.gameMap)
	s.rooms[room.ID] = room
	return response, nil
}

func (s *Store) clearRooms() clearRoomsResponse {
	registered := s.registeredRooms()
	deleted := 0
	var resources roomResources
	for _, room := range registered {
		room.mu.Lock()
		playerIDs, removed := resources.removeRoomLocked(room)
		room.mu.Unlock()
		if removed && s.deleteRoomIfSame(room.ID, room) {
			s.releasePlayerIDs(playerIDs)
			deleted++
		}
	}

	resources.close(defaultRoomDebugDeleteMsg)
	return clearRoomsResponse{Deleted: deleted}
}

func (s *Store) deleteRoom(roomID string) (clearRoomsResponse, bool) {
	room := s.lookupRoom(roomID)
	if room == nil {
		return clearRoomsResponse{}, false
	}

	var resources roomResources
	room.mu.Lock()
	playerIDs, removed := resources.removeRoomLocked(room)
	room.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected == nil || s.rooms[roomID] != expected {
		return false
	}
	delete(s.rooms, roomID)
	return true
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
	room := s.lookupRoom(roomID)
	if room == nil {
		return playerSessionResponse{}, ErrRoomNotFound
	}

	credentials, err := s.issuePlayerCredentials()
	if err != nil {
		return playerSessionResponse{}, err
	}

	room.mu.Lock()
	if room.removed {
		room.mu.Unlock()
		s.releasePlayerID(credentials.id)
		return playerSessionResponse{}, ErrRoomNotFound
	}
	if len(room.Players) >= s.debugRoomCapacity() {
		room.mu.Unlock()
		s.releasePlayerID(credentials.id)
		return playerSessionResponse{}, ErrRoomFull
	}
	issued := s.addPlayerLocked(room, credentials)
	room.mu.Unlock()
	return issued, nil
}

func (s *Store) joinMatchmaking() (matchmakingJoinResponse, error) {
	var credentials *playerCredentials
	for _, room := range s.registeredRooms() {
		room.mu.Lock()
		canJoin := room.canAcceptMatchmakingLocked(s.debugRoomCapacity(), s.matchCapacity())
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

	response, err := s.createMatchmakingRoom(credentials)
	if errors.Is(err, ErrActiveRoomCapReached) {
		s.cleanupExpired(s.clock.Now())
		response, err = s.createMatchmakingRoom(credentials)
	}
	if err != nil && credentials != nil {
		s.releasePlayerID(credentials.id)
	}
	return response, err
}

func (s *Store) tryJoinMatchmakingRoom(room *room, credentials playerCredentials) (matchmakingJoinResponse, bool) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if !room.canAcceptMatchmakingLocked(s.debugRoomCapacity(), s.matchCapacity()) {
		return matchmakingJoinResponse{}, false
	}

	issued := s.addPlayerLocked(room, credentials)
	s.markRoomMatchedIfFullLocked(room)
	return matchmakingJoinResponseFrom(room.toResponse(s.gameMap), issued), true
}

func (s *Store) createMatchmakingRoom(credentials *playerCredentials) (matchmakingJoinResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return matchmakingJoinResponse{}, ErrInternal
	}
	if len(s.rooms) >= s.maxActiveRooms {
		return matchmakingJoinResponse{}, ErrActiveRoomCapReached
	}
	roomID, err := s.uniqueRoomIDLocked()
	if err != nil {
		return matchmakingJoinResponse{}, err
	}
	if credentials == nil {
		issued, err := s.issuePlayerCredentialsLocked()
		if err != nil {
			return matchmakingJoinResponse{}, err
		}
		credentials = &issued
	}

	room := s.newRoomLocked(roomID)
	issued := s.addPlayerLocked(room, *credentials)
	s.markRoomMatchedIfFullLocked(room)
	response := matchmakingJoinResponseFrom(room.toResponse(s.gameMap), issued)
	s.rooms[room.ID] = room
	return response, nil
}

func (s *Store) markRoomMatchedIfFullLocked(room *room) {
	if len(room.Players) != s.matchCapacity() {
		return
	}
	room.matchStatus = MatchStatusMatched
	room.readyPlayers = make(map[string]bool)
	room.lastActivityAt = s.clock.Now()
}

func matchmakingJoinResponseFrom(room roomResponse, issued playerSessionResponse) matchmakingJoinResponse {
	return matchmakingJoinResponse{
		Room:          room,
		Player:        issued.Player,
		SessionToken:  issued.SessionToken,
		WebSocketPath: issued.WebSocketPath,
	}
}

func (r *room) canAcceptMatchmakingLocked(debugCapacity int, matchCapacity int) bool {
	return !r.removed &&
		r.Status == RoomStatusWaiting &&
		r.matchStatus == "" &&
		len(r.Players) < debugCapacity &&
		len(r.Players) < matchCapacity
}

func (s *Store) debugRoomCapacity() int {
	return s.gameMap.MaxPlayers
}

func (s *Store) matchCapacity() int {
	return s.gameConfig.MatchPlayerCount()
}

func (s *Store) startRoom(roomID string) (roomResponse, error) {
	room := s.lookupRoom(roomID)
	if room == nil {
		return roomResponse{}, ErrRoomNotFound
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.removed {
		return roomResponse{}, ErrRoomNotFound
	}
	if len(room.Players) == 0 {
		return roomResponse{}, ErrRoomHasNoPlayers
	}

	s.startRoomLocked(room)
	return room.toResponse(s.gameMap), nil
}

func (s *Store) newRoomLocked(roomID string) *room {
	now := s.clock.Now()
	room := &room{
		ID:             roomID,
		Status:         RoomStatusWaiting,
		sessions:       make(map[string]playerSession),
		pendingInputs:  make(map[string]simulation.InputCommand),
		clients:        make(map[string]*clientSession),
		reservations:   make(map[string]*clientReservation),
		createdAt:      now,
		lastActivityAt: now,
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
	team, slot := s.playerAssignmentForIndex(playerIndex)
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
		room.state = simulation.NewStateWithConfig(simulationPlayers(room.Players, s.gameConfig), simulation.Config{Game: s.gameConfig})
	}
	if room.ticker == nil {
		room.ticker = s.clock.NewTicker(time.Second / time.Duration(s.gameConfig.TickRate))
		room.stop = make(chan struct{})
		go s.runRoom(room, room.ticker, room.stop)
	}
}

func (s *Store) playerAssignmentForIndex(playerIndex int) (simulation.Team, int) {
	team, slot, ok := s.gameConfig.TeamForPlayerIndex(playerIndex)
	if !ok {
		return simulation.TeamRed, playerIndex
	}
	return team, slot
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
