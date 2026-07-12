package rooms

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

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
				if errors.Is(err, ErrRoomFull) {
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
				if errors.Is(err, ErrRoomNotFound) {
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, errorResponse{Error: apiError{Code: code, Message: message}})
}
