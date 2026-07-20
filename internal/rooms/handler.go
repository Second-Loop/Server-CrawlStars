package rooms

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const matchmakingJoinRequestBodyLimit int64 = 1024

type HandlerConfig struct {
	EnableDebugAPI       bool
	DebugAPIToken        string
	JoinLimiter          *IPRateLimiter
	TrustedProxyPrefixes []netip.Prefix
}

func Handler(store *Store) http.Handler {
	handler, err := HandlerWithConfig(store, HandlerConfig{})
	if err != nil {
		panic(err)
	}
	return handler
}

func HandlerWithConfig(store *Store, config HandlerConfig) (http.Handler, error) {
	if config.EnableDebugAPI && strings.TrimSpace(config.DebugAPIToken) == "" {
		return nil, fmt.Errorf("debug API token is required when debug API is enabled")
	}
	if store == nil {
		store = NewStore(5)
	}

	debugAPIEnabled := config.EnableDebugAPI
	debugTokenDigest := sha256.Sum256([]byte(config.DebugAPIToken))
	joinLimiter := config.JoinLimiter
	if joinLimiter == nil {
		joinLimiter = NewIPRateLimiter(DefaultJoinRatePerMinute, DefaultJoinBurst, nil)
	}
	trustedProxyPrefixes := append([]netip.Prefix(nil), config.TrustedProxyPrefixes...)
	debugGuard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !authorizeDebugRequest(w, r, debugAPIEnabled, debugTokenDigest) {
				return
			}
			next(w, r)
		}
	}
	router := newRouterWithDebugGuard(store, debugGuard, joinLimiter, trustedProxyPrefixes)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "//") {
			if strings.HasPrefix(r.URL.Path, "/rooms//") {
				if !authorizeDebugRequest(w, r, debugAPIEnabled, debugTokenDigest) {
					return
				}
				writeRoomNotFound(w)
				return
			}
			writeRouteNotFound(w)
			return
		}

		decodedRequest := requestWithDecodedPathSegments(r)
		router.ServeHTTP(w, decodedRequest)
	}), nil
}

func authorizeDebugRequest(w http.ResponseWriter, r *http.Request, enabled bool, expectedTokenDigest [sha256.Size]byte) bool {
	if !enabled {
		writeRouteNotFound(w)
		return false
	}

	authorization := r.Header.Values("Authorization")
	const bearerPrefix = "Bearer "
	if len(authorization) != 1 || len(authorization[0]) <= len(bearerPrefix) ||
		!strings.EqualFold(authorization[0][:len(bearerPrefix)], bearerPrefix) {
		writeUnauthorized(w)
		return false
	}
	candidateDigest := sha256.Sum256([]byte(authorization[0][len(bearerPrefix):]))
	if subtle.ConstantTimeCompare(candidateDigest[:], expectedTokenDigest[:]) != 1 {
		writeUnauthorized(w)
		return false
	}
	return true
}

func newRouter(store *Store) *http.ServeMux {
	return newRouterWithDebugGuard(store, func(handler http.HandlerFunc) http.HandlerFunc {
		return handler
	}, NewIPRateLimiter(DefaultJoinRatePerMinute, DefaultJoinBurst, nil), nil)
}

func newRouterWithDebugGuard(
	store *Store,
	debugGuard func(http.HandlerFunc) http.HandlerFunc,
	joinLimiter *IPRateLimiter,
	trustedProxyPrefixes []netip.Prefix,
) *http.ServeMux {
	mux := http.NewServeMux()
	debugHandleFunc := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, debugGuard(handler))
	}

	mux.HandleFunc("POST /matchmaking/join", func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter := joinLimiter.Allow(clientIP(r, trustedProxyPrefixes))
		if !allowed {
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds(retryAfter), 10))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, matchmakingJoinRequestBodyLimit)
		joinRequest, err := decodeMatchmakingJoinRequest(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", ErrInvalidRequest.Error())
			return
		}
		if joinRequest.GameMode == "" {
			joinRequest.GameMode = store.defaultGameMode()
		}
		result, err := store.joinMatchmakingRequest(joinRequest.GameMode, joinRequest.CharacterType)
		if err != nil {
			if errors.Is(err, ErrInvalidGameMode) {
				writeError(w, http.StatusBadRequest, "invalid_game_mode", err.Error())
				return
			}
			if errors.Is(err, ErrInvalidCharacterType) {
				writeError(w, http.StatusBadRequest, "invalid_character_type", err.Error())
				return
			}
			if errors.Is(err, ErrInternal) {
				writeInternalError(w)
				return
			}
			writeError(w, http.StatusConflict, "room_cap_reached", err.Error())
			return
		}
		if result.CharacterTypeDefaulted {
			store.logCharacterTypeDefaulted(result.Response.GameMode)
		}
		writeJSON(w, http.StatusCreated, result.Response)
	})
	mux.HandleFunc("HEAD /matchmaking/join", writeMethodNotAllowed)
	mux.HandleFunc("/matchmaking/join", writeMethodNotAllowed)

	debugHandleFunc("GET /rooms", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, store.listRooms())
	})
	debugHandleFunc("HEAD /rooms", writeMethodNotAllowed)
	debugHandleFunc("POST /rooms", func(w http.ResponseWriter, _ *http.Request) {
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
	debugHandleFunc("DELETE /rooms", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, store.clearRooms())
	})
	debugHandleFunc("/rooms", writeMethodNotAllowed)
	debugHandleFunc("/rooms/{$}", func(w http.ResponseWriter, _ *http.Request) {
		writeRoomNotFound(w)
	})

	debugHandleFunc("GET /rooms/{roomID}", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID") {
			return
		}
		roomID := r.PathValue("roomID")
		found, ok := store.getRoomResponse(roomID)
		if !ok {
			writeRoomNotFound(w)
			return
		}
		writeJSON(w, http.StatusOK, found)
	})
	debugHandleFunc("HEAD /rooms/{roomID}", pathMethodNotAllowed("roomID"))
	debugHandleFunc("DELETE /rooms/{roomID}", func(w http.ResponseWriter, r *http.Request) {
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
	debugHandleFunc("/rooms/{roomID}", pathMethodNotAllowed("roomID"))

	debugHandleFunc("POST /rooms/{roomID}/players", func(w http.ResponseWriter, r *http.Request) {
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
	debugHandleFunc("HEAD /rooms/{roomID}/players", pathMethodNotAllowed("roomID"))
	debugHandleFunc("/rooms/{roomID}/players", pathMethodNotAllowed("roomID"))

	debugHandleFunc("POST /rooms/{roomID}/start", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID") {
			return
		}
		roomID := r.PathValue("roomID")
		started, err := store.startRoom(roomID)
		if err != nil {
			if errors.Is(err, ErrInternal) {
				writeInternalError(w)
				return
			}
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
	debugHandleFunc("HEAD /rooms/{roomID}/start", pathMethodNotAllowed("roomID"))
	debugHandleFunc("/rooms/{roomID}/start", pathMethodNotAllowed("roomID"))

	mux.HandleFunc("GET /rooms/{roomID}/players/{playerID}", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID", "playerID") {
			return
		}
		store.handleWebSocket(w, r, r.PathValue("roomID"), r.PathValue("playerID"))
	})
	debugHandleFunc("HEAD /rooms/{roomID}/players/{playerID}", pathMethodNotAllowed("roomID", "playerID"))
	debugHandleFunc("/rooms/{roomID}/players/{playerID}", pathMethodNotAllowed("roomID", "playerID"))

	mux.HandleFunc("GET /rooms/{roomID}/players/{$}", func(w http.ResponseWriter, r *http.Request) {
		if rejectSlashPathValues(w, r, "roomID") {
			return
		}
		store.handleWebSocket(w, r, r.PathValue("roomID"), "")
	})
	debugHandleFunc("HEAD /rooms/{roomID}/players/{$}", pathMethodNotAllowed("roomID"))
	debugHandleFunc("/rooms/{roomID}/players/{$}", pathMethodNotAllowed("roomID"))

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeRouteNotFound(w)
	})
	return mux
}

func decodeMatchmakingJoinRequest(body io.Reader) (matchmakingJoinRequest, error) {
	decoder := json.NewDecoder(body)
	var rawRequest json.RawMessage
	if err := decoder.Decode(&rawRequest); err != nil {
		if errors.Is(err, io.EOF) {
			return matchmakingJoinRequest{}, nil
		}
		return matchmakingJoinRequest{}, ErrInvalidRequest
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return matchmakingJoinRequest{}, ErrInvalidRequest
	}
	if bytes.Equal(bytes.TrimSpace(rawRequest), []byte("null")) {
		return matchmakingJoinRequest{}, ErrInvalidRequest
	}

	var fields struct {
		GameMode      json.RawMessage `json:"gameMode"`
		CharacterType json.RawMessage `json:"characterType"`
	}
	if err := json.Unmarshal(rawRequest, &fields); err != nil {
		return matchmakingJoinRequest{}, ErrInvalidRequest
	}
	request := matchmakingJoinRequest{CharacterType: fields.CharacterType}
	if fields.GameMode == nil {
		return request, nil
	}
	if bytes.Equal(bytes.TrimSpace(fields.GameMode), []byte("null")) {
		return matchmakingJoinRequest{}, ErrInvalidRequest
	}

	if err := json.Unmarshal(fields.GameMode, &request.GameMode); err != nil {
		return matchmakingJoinRequest{}, ErrInvalidRequest
	}
	return request, nil
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

func writeUnauthorized(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, errorResponse{Error: apiError{Code: code, Message: message}})
}
