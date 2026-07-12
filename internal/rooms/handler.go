package rooms

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func Handler(store *Store) http.Handler {
	if store == nil {
		store = NewStore(5)
	}

	router := newRouter(store)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "//") {
			if strings.HasPrefix(r.URL.Path, "/rooms//") {
				writeRoomNotFound(w)
				return
			}
			writeRouteNotFound(w)
			return
		}
		router.ServeHTTP(w, requestWithDecodedPathSegments(r))
	})
}

func newRouter(store *Store) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /matchmaking/join", func(w http.ResponseWriter, _ *http.Request) {
		joined, err := store.joinMatchmaking()
		if err != nil {
			if errors.Is(err, ErrInternal) {
				writeInternalError(w)
				return
			}
			writeError(w, http.StatusConflict, "room_cap_reached", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, joined)
	})
	mux.HandleFunc("HEAD /matchmaking/join", writeMethodNotAllowed)
	mux.HandleFunc("/matchmaking/join", writeMethodNotAllowed)

	mux.HandleFunc("GET /rooms", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, store.listRooms())
	})
	mux.HandleFunc("HEAD /rooms", writeMethodNotAllowed)
	mux.HandleFunc("POST /rooms", func(w http.ResponseWriter, _ *http.Request) {
		created, err := store.createRoom()
		if err != nil {
			if errors.Is(err, ErrInternal) {
				writeInternalError(w)
				return
			}
			writeError(w, http.StatusConflict, "room_cap_reached", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, created)
	})
	mux.HandleFunc("DELETE /rooms", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, store.clearRooms())
	})
	mux.HandleFunc("/rooms", writeMethodNotAllowed)
	mux.HandleFunc("/rooms/{$}", func(w http.ResponseWriter, _ *http.Request) {
		writeRoomNotFound(w)
	})

	mux.HandleFunc("GET /rooms/{roomID}", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID") {
			return
		}
		roomID := r.PathValue("roomID")
		found, ok := store.getRoom(roomID)
		if !ok {
			writeRoomNotFound(w)
			return
		}
		writeJSON(w, http.StatusOK, found)
	})
	mux.HandleFunc("HEAD /rooms/{roomID}", pathMethodNotAllowed("roomID"))
	mux.HandleFunc("DELETE /rooms/{roomID}", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID") {
			return
		}
		roomID := r.PathValue("roomID")
		deleted, ok := store.deleteRoom(roomID)
		if !ok {
			writeRoomNotFound(w)
			return
		}
		writeJSON(w, http.StatusOK, deleted)
	})
	mux.HandleFunc("/rooms/{roomID}", pathMethodNotAllowed("roomID"))

	mux.HandleFunc("POST /rooms/{roomID}/players", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID") {
			return
		}
		roomID := r.PathValue("roomID")
		player, err := store.addPlayer(roomID)
		if err != nil {
			if errors.Is(err, ErrInternal) {
				writeInternalError(w)
				return
			}
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
	})
	mux.HandleFunc("HEAD /rooms/{roomID}/players", pathMethodNotAllowed("roomID"))
	mux.HandleFunc("/rooms/{roomID}/players", pathMethodNotAllowed("roomID"))

	mux.HandleFunc("POST /rooms/{roomID}/start", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID") {
			return
		}
		roomID := r.PathValue("roomID")
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
	})
	mux.HandleFunc("HEAD /rooms/{roomID}/start", pathMethodNotAllowed("roomID"))
	mux.HandleFunc("/rooms/{roomID}/start", pathMethodNotAllowed("roomID"))

	mux.HandleFunc("GET /rooms/{roomID}/players/{playerID}", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID", "playerID") {
			return
		}
		store.handleWebSocket(w, r, r.PathValue("roomID"), r.PathValue("playerID"))
	})
	mux.HandleFunc("HEAD /rooms/{roomID}/players/{playerID}", pathMethodNotAllowed("roomID", "playerID"))
	mux.HandleFunc("/rooms/{roomID}/players/{playerID}", pathMethodNotAllowed("roomID", "playerID"))

	mux.HandleFunc("GET /rooms/{roomID}/players/{$}", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID") {
			return
		}
		store.handleWebSocket(w, r, r.PathValue("roomID"), "")
	})
	mux.HandleFunc("HEAD /rooms/{roomID}/players/{$}", pathMethodNotAllowed("roomID"))
	mux.HandleFunc("/rooms/{roomID}/players/{$}", pathMethodNotAllowed("roomID"))

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeRouteNotFound(w)
	})
	return mux
}

func requestWithDecodedPathSegments(r *http.Request) *http.Request {
	rawPath := rawPathFromDecodedPath(r.URL.Path)
	if rawPath == r.URL.EscapedPath() {
		return r
	}

	clonedRequest := r.Clone(r.Context())
	clonedURL := *r.URL
	clonedURL.RawPath = rawPath
	clonedRequest.URL = &clonedURL
	return clonedRequest
}

func rawPathFromDecodedPath(decodedPath string) string {
	var rawPath strings.Builder
	rawPath.Grow(len(decodedPath))

	segmentStart := 0
	for index := 0; index <= len(decodedPath); index++ {
		if index < len(decodedPath) && decodedPath[index] != '/' {
			continue
		}

		segment := decodedPath[segmentStart:index]
		switch segment {
		case ".":
			rawPath.WriteString("%2e")
		case "..":
			rawPath.WriteString("%2e%2e")
		default:
			rawPath.WriteString(url.PathEscape(segment))
		}
		if index < len(decodedPath) {
			rawPath.WriteByte('/')
		}
		segmentStart = index + 1
	}
	return rawPath.String()
}

func pathMethodNotAllowed(pathValueNames ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, pathValueNames...) {
			return
		}
		writeMethodNotAllowed(w, r)
	}
}

func rejectSlashPathValues(w http.ResponseWriter, r *http.Request, pathValueNames ...string) bool {
	for _, name := range pathValueNames {
		if strings.Contains(r.PathValue(name), "/") {
			writeRouteNotFound(w)
			return true
		}
	}
	return false
}

func writeMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func writeRoomNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "room_not_found", "room not found")
}

func writeRouteNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "route not found")
}

func writeInternalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, errorResponse{Error: apiError{Code: code, Message: message}})
}
