package rooms

import (
	"crypto/rand"
	"crypto/sha256"
	"io"
	"sync"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

type Store struct {
	mu             sync.Mutex
	maxActiveRooms int
	rooms          map[string]*room
	random         io.Reader
	clock          clock
	gameMap        simulation.MapData
	gameConfig     simulation.GameConfig
	closed         bool
}

type StoreConfig struct {
	Map        simulation.MapData
	GameConfig simulation.GameConfig
	Random     io.Reader
}

type room struct {
	ID              string
	Status          RoomStatus
	Players         []playerResponse
	matchStatus     MatchStatus
	readyPlayers    map[string]bool
	sessions        map[string]playerSession
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

	return &Store{
		maxActiveRooms: maxActiveRooms,
		rooms:          make(map[string]*room),
		random:         random,
		clock:          clock,
		gameMap:        resolvedConfig.Map,
		gameConfig:     resolvedConfig,
	}
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

func (s *Store) createRoom() (roomResponse, error) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.rooms) >= s.maxActiveRooms {
		return roomResponse{}, ErrActiveRoomCapReached
	}

	roomID, err := s.uniqueRoomIDLocked()
	if err != nil {
		return roomResponse{}, err
	}
	room := s.newRoomLocked(roomID)
	s.rooms[room.ID] = room
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

func (s *Store) addPlayer(roomID string) (playerSessionResponse, error) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return playerSessionResponse{}, ErrRoomNotFound
	}
	if len(room.Players) >= s.debugRoomCapacity() {
		return playerSessionResponse{}, ErrRoomFull
	}

	issued, session, err := s.preparePlayerLocked(room)
	if err != nil {
		return playerSessionResponse{}, err
	}
	s.addPlayerLocked(room, issued.Player, session)
	return issued, nil
}

func (s *Store) joinMatchmaking() (matchmakingJoinResponse, error) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	room := s.findWaitingRoomWithCapacity()
	newRoom := false
	if room == nil {
		if len(s.rooms) >= s.maxActiveRooms {
			return matchmakingJoinResponse{}, ErrActiveRoomCapReached
		}
		roomID, err := s.uniqueRoomIDLocked()
		if err != nil {
			return matchmakingJoinResponse{}, err
		}
		room = s.newRoomLocked(roomID)
		newRoom = true
	}

	issued, session, err := s.preparePlayerLocked(room)
	if err != nil {
		return matchmakingJoinResponse{}, err
	}
	if newRoom {
		s.rooms[room.ID] = room
	}
	s.addPlayerLocked(room, issued.Player, session)
	if len(room.Players) == s.matchCapacity() {
		room.matchStatus = MatchStatusMatched
		room.readyPlayers = make(map[string]bool)
		room.lastActivityAt = s.clock.Now()
	}

	roomResponse := room.toResponse(s.gameMap)
	return matchmakingJoinResponse{
		Room:          roomResponse,
		Player:        issued.Player,
		SessionToken:  issued.SessionToken,
		WebSocketPath: issued.WebSocketPath,
	}, nil
}

func (s *Store) findWaitingRoomWithCapacity() *room {
	for _, room := range s.rooms {
		if room.Status == RoomStatusWaiting && room.matchStatus == "" && len(room.Players) < s.debugRoomCapacity() && len(room.Players) < s.matchCapacity() {
			return room
		}
	}
	return nil
}

func (s *Store) debugRoomCapacity() int {
	return s.gameMap.MaxPlayers
}

func (s *Store) matchCapacity() int {
	return s.gameConfig.MatchPlayerCount()
}

func (s *Store) startRoom(roomID string) (roomResponse, error) {
	s.cleanupExpired()

	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
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
		clients:        make(map[string]*websocket.Conn),
		createdAt:      now,
		lastActivityAt: now,
	}
	return room
}

func (s *Store) preparePlayerLocked(room *room) (playerSessionResponse, playerSession, error) {
	playerID, err := s.uniquePlayerIDLocked()
	if err != nil {
		return playerSessionResponse{}, playerSession{}, err
	}
	sessionToken, err := randomValue(s.random, "", sessionRandomBytes)
	if err != nil {
		return playerSessionResponse{}, playerSession{}, ErrInternal
	}
	playerIndex := len(room.Players)
	team, slot := s.playerAssignmentForIndex(playerIndex)
	player := playerResponse{
		ID:   playerID,
		Team: string(team),
		Slot: slot,
	}
	return playerSessionResponse{
		Player:        player,
		SessionToken:  sessionToken,
		WebSocketPath: webSocketPath(room.ID, player.ID, sessionToken),
	}, playerSession{digest: sha256.Sum256([]byte(sessionToken))}, nil
}

func (s *Store) addPlayerLocked(room *room, player playerResponse, session playerSession) {
	room.Players = append(room.Players, player)
	room.sessions[player.ID] = session
	room.lastActivityAt = s.clock.Now()
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
		if !s.hasPlayerLocked(playerID) {
			return playerID, nil
		}
	}
	return "", ErrInternal
}

func (s *Store) hasPlayerLocked(playerID string) bool {
	for _, room := range s.rooms {
		if room.hasPlayer(playerID) {
			return true
		}
	}
	return false
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
		go s.runRoom(room.ID, room.ticker, room.stop)
	}
}

func (s *Store) playerAssignmentForIndex(playerIndex int) (simulation.Team, int) {
	team, slot, ok := s.gameConfig.TeamForPlayerIndex(playerIndex)
	if !ok {
		return simulation.TeamRed, playerIndex
	}
	return team, slot
}

func (r *room) hasPlayer(playerID string) bool {
	for _, player := range r.Players {
		if player.ID == playerID {
			return true
		}
	}
	return false
}
