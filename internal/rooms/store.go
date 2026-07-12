package rooms

import (
	"strconv"
	"sync"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

type Store struct {
	mu             sync.Mutex
	maxActiveRooms int
	nextRoomSeq    int
	nextPlayerSeq  int
	rooms          map[string]*room
	clock          clock
	gameMap        simulation.MapData
	gameConfig     simulation.GameConfig
	closed         bool
}

type StoreConfig struct {
	Map        simulation.MapData
	GameConfig simulation.GameConfig
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
		return playerResponse{}, ErrRoomNotFound
	}
	if len(room.Players) >= s.debugRoomCapacity() {
		return playerResponse{}, ErrRoomFull
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
			return matchmakingJoinResponse{}, ErrActiveRoomCapReached
		}
		room = s.createRoomLocked()
	}

	player := s.addPlayerLocked(room)
	if len(room.Players) == s.matchCapacity() {
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

func (s *Store) createRoomLocked() *room {
	now := s.clock.Now()
	s.nextRoomSeq++
	room := &room{
		ID:             "room-" + strconv.Itoa(s.nextRoomSeq),
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
	team, slot := s.playerAssignmentForIndex(playerIndex)
	player := playerResponse{
		ID:   "player-" + strconv.Itoa(s.nextPlayerSeq),
		Team: string(team),
		Slot: slot,
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
