package rooms

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
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
}

type room struct {
	ID      string
	Status  RoomStatus
	Players []playerResponse
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

func NewStore(maxActiveRooms int) *Store {
	if maxActiveRooms <= 0 {
		maxActiveRooms = 5
	}

	return &Store{
		maxActiveRooms: maxActiveRooms,
		rooms:          make(map[string]*room),
	}
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

func (s *Store) createRoom() (roomResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.rooms) >= s.maxActiveRooms {
		return roomResponse{}, errString("active room cap reached")
	}

	s.nextRoomSeq++
	room := &room{
		ID:     "room-" + itoa(s.nextRoomSeq),
		Status: RoomStatusWaiting,
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
	return room.toResponse(), nil
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
