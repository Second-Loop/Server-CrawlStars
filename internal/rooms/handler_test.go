package rooms

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

func TestStoreCreatesOpaqueIDsAndSessionSecrets(t *testing.T) {
	random := bytes.NewReader(bytes.Join([][]byte{
		bytes.Repeat([]byte{0x01}, 16),
		bytes.Repeat([]byte{0x02}, 16),
		bytes.Repeat([]byte{0x03}, 32),
		bytes.Repeat([]byte{0x04}, 16),
		bytes.Repeat([]byte{0x05}, 32),
		bytes.Repeat([]byte{0x06}, 16),
	}, nil))
	store := NewStoreWithConfig(5, StoreConfig{Random: random})
	defer store.Close()

	firstRoom, err := store.createRoom()
	if err != nil {
		t.Fatalf("create first room: %v", err)
	}
	firstPlayer, err := store.addPlayer(firstRoom.ID)
	if err != nil {
		t.Fatalf("add first player: %v", err)
	}
	secondPlayer, err := store.addPlayer(firstRoom.ID)
	if err != nil {
		t.Fatalf("add second player: %v", err)
	}
	secondRoom, err := store.createRoom()
	if err != nil {
		t.Fatalf("create second room: %v", err)
	}

	assertOpaqueID(t, firstRoom.ID, "room_", 16)
	assertOpaqueID(t, secondRoom.ID, "room_", 16)
	assertOpaqueID(t, firstPlayer.Player.ID, "player_", 16)
	assertOpaqueID(t, secondPlayer.Player.ID, "player_", 16)
	if firstRoom.ID == secondRoom.ID {
		t.Fatalf("expected distinct room IDs, got %q", firstRoom.ID)
	}
	if firstPlayer.Player.ID == secondPlayer.Player.ID {
		t.Fatalf("expected distinct player IDs, got %q", firstPlayer.Player.ID)
	}
	assertOpaqueID(t, firstPlayer.SessionToken, "", 32)
	assertOpaqueID(t, secondPlayer.SessionToken, "", 32)
	if firstPlayer.SessionToken == secondPlayer.SessionToken {
		t.Fatal("expected distinct player session tokens")
	}

	wantDigest := sha256.Sum256([]byte(firstPlayer.SessionToken))
	storedRoom := store.lookupRoom(firstRoom.ID)
	storedRoom.mu.Lock()
	storedSession := storedRoom.sessions[firstPlayer.Player.ID]
	storedRoom.mu.Unlock()
	if storedSession.digest != wantDigest {
		t.Fatal("expected only the issued session digest in room state")
	}
}

func TestHandlerIssuesSessionSecretWithoutPublicLeak(t *testing.T) {
	random := bytes.NewReader(bytes.Join([][]byte{
		bytes.Repeat([]byte{0x11}, 16),
		bytes.Repeat([]byte{0x12}, 16),
		bytes.Repeat([]byte{0x13}, 32),
	}, nil))
	store := NewStoreWithConfig(5, StoreConfig{Random: random})
	defer store.Close()
	handler := debugHandler(t, store)

	room := createRoom(t, handler)
	rec := request(handler, http.MethodPost, "/rooms/"+room.ID+"/players")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected create player status 201, got %d", rec.Code)
	}
	var issued playerSessionResponse
	decodeResponse(t, rec, &issued)
	assertOpaqueID(t, issued.Player.ID, "player_", 16)
	assertOpaqueID(t, issued.SessionToken, "", 32)
	wantPath := "/rooms/" + room.ID + "/players/" + issued.Player.ID + "?token=" + issued.SessionToken
	if issued.WebSocketPath != wantPath {
		t.Fatal("expected websocket path to match the issued room, player, and session")
	}

	storedRoom := store.lookupRoom(room.ID)
	storedRoom.mu.Lock()
	ready := readyEventMessage{
		Type:    "Ready",
		Map:     mapResponseFromSimulation(store.gameConfig.Map),
		Players: readyEventPlayers(storedRoom.Players, store.gameConfig),
	}
	snapshot := storedRoom.matchSnapshotMessage(MatchStatusMatched, 0)
	storedRoom.mu.Unlock()

	responses := map[string][]byte{
		"room list":   request(handler, http.MethodGet, "/rooms").Body.Bytes(),
		"room detail": request(handler, http.MethodGet, "/rooms/"+room.ID).Body.Bytes(),
		"Ready":       mustMarshalTestJSON(t, ready),
		"snapshot":    mustMarshalTestJSON(t, snapshot),
	}
	for name, payload := range responses {
		if bytes.Contains(payload, []byte(issued.SessionToken)) {
			t.Fatalf("expected %s to omit the raw session token", name)
		}
		if bytes.Contains(payload, []byte("sessionToken")) || bytes.Contains(payload, []byte("digest")) {
			t.Fatalf("expected %s to omit session fields", name)
		}
	}
}

func TestHandlerSessionSecretFailureIsAtomic(t *testing.T) {
	t.Run("reader error", func(t *testing.T) {
		readerErr := errors.New("entropy source private detail")
		store := NewStoreWithConfig(5, StoreConfig{Random: iotest.ErrReader(readerErr)})
		defer store.Close()

		rec := request(debugHandler(t, store), http.MethodPost, "/rooms")
		if strings.Contains(rec.Body.String(), readerErr.Error()) {
			t.Fatalf("expected response to omit reader error details, got %s", rec.Body.String())
		}
		assertInternalError(t, rec)
		if got := len(store.listRooms().Rooms); got != 0 {
			t.Fatalf("expected no partial room, got %d", got)
		}
	})

	t.Run("room ID short read", func(t *testing.T) {
		store := NewStoreWithConfig(5, StoreConfig{Random: bytes.NewReader(make([]byte, 15))})
		defer store.Close()
		handler := debugHandler(t, store)

		assertInternalError(t, request(handler, http.MethodPost, "/rooms"))
		if got := len(store.listRooms().Rooms); got != 0 {
			t.Fatalf("expected no partial room, got %d", got)
		}
	})

	t.Run("player token short read", func(t *testing.T) {
		random := bytes.NewReader(bytes.Join([][]byte{
			bytes.Repeat([]byte{0x21}, 16),
			bytes.Repeat([]byte{0x22}, 16),
			bytes.Repeat([]byte{0x23}, 31),
		}, nil))
		store := NewStoreWithConfig(5, StoreConfig{Random: random})
		defer store.Close()
		handler := debugHandler(t, store)
		room := createRoom(t, handler)

		assertInternalError(t, request(handler, http.MethodPost, "/rooms/"+room.ID+"/players"))
		assertRoomHasPlayerAndSessionCount(t, store, room.ID, 0)
	})

	t.Run("matchmaking token short read", func(t *testing.T) {
		random := bytes.NewReader(bytes.Join([][]byte{
			bytes.Repeat([]byte{0x31}, 16),
			bytes.Repeat([]byte{0x32}, 16),
			bytes.Repeat([]byte{0x33}, 31),
		}, nil))
		store := NewStoreWithConfig(5, StoreConfig{Random: random})
		defer store.Close()

		assertInternalError(t, request(debugHandler(t, store), http.MethodPost, "/matchmaking/join"))
		if got := len(store.listRooms().Rooms); got != 0 {
			t.Fatalf("expected failed matchmaking to leave no room, got %d", got)
		}
	})
}

func TestStoreOpaqueIDCollisionExhaustionIsAtomic(t *testing.T) {
	t.Run("room ID", func(t *testing.T) {
		candidate := bytes.Repeat([]byte{0x41}, 16)
		store := NewStoreWithConfig(5, StoreConfig{Random: bytes.NewReader(bytes.Repeat(candidate, 9))})
		defer store.Close()
		handler := debugHandler(t, store)

		_ = createRoom(t, handler)
		assertInternalError(t, request(handler, http.MethodPost, "/rooms"))
		if got := len(store.listRooms().Rooms); got != 1 {
			t.Fatalf("expected one original room after collision exhaustion, got %d", got)
		}
	})

	t.Run("player ID", func(t *testing.T) {
		roomID := bytes.Repeat([]byte{0x51}, 16)
		playerID := bytes.Repeat([]byte{0x52}, 16)
		token := bytes.Repeat([]byte{0x53}, 32)
		random := bytes.NewReader(bytes.Join([][]byte{
			roomID,
			playerID,
			token,
			bytes.Repeat(playerID, 8),
		}, nil))
		store := NewStoreWithConfig(5, StoreConfig{Random: random})
		defer store.Close()
		handler := debugHandler(t, store)
		room := createRoom(t, handler)
		_ = createPlayer(t, handler, room.ID)

		assertInternalError(t, request(handler, http.MethodPost, "/rooms/"+room.ID+"/players"))
		assertRoomHasPlayerAndSessionCount(t, store, room.ID, 1)
	})
}

func TestStoreReturnsTypedErrors(t *testing.T) {
	t.Run("active room cap from create", func(t *testing.T) {
		store := NewStore(1)
		defer store.Close()

		if _, err := store.createRoom(); err != nil {
			t.Fatalf("create first room: %v", err)
		}
		_, err := store.createRoom()
		if !errors.Is(err, ErrActiveRoomCapReached) {
			t.Fatalf("expected ErrActiveRoomCapReached, got %v", err)
		}
	})

	t.Run("active room cap from matchmaking", func(t *testing.T) {
		store := NewStore(1)
		defer store.Close()

		room, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		for range store.matchCapacity() {
			if _, err := store.addPlayer(room.ID); err != nil {
				t.Fatalf("fill matchmaking room: %v", err)
			}
		}
		_, err = store.joinMatchmaking(store.defaultGameMode())
		if !errors.Is(err, ErrActiveRoomCapReached) {
			t.Fatalf("expected ErrActiveRoomCapReached, got %v", err)
		}
	})

	t.Run("missing room", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()

		if _, err := store.addPlayer("missing"); !errors.Is(err, ErrRoomNotFound) {
			t.Fatalf("add player: expected ErrRoomNotFound, got %v", err)
		}
		if _, err := store.startRoom("missing"); !errors.Is(err, ErrRoomNotFound) {
			t.Fatalf("start room: expected ErrRoomNotFound, got %v", err)
		}
		if _, err := store.reserveClient("missing", "player-1", nil); !errors.Is(err, ErrRoomNotFound) {
			t.Fatalf("reserve client: expected ErrRoomNotFound, got %v", err)
		}
	})

	t.Run("room full", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()

		room, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		for range store.debugRoomCapacity() {
			if _, err := store.addPlayer(room.ID); err != nil {
				t.Fatalf("fill room: %v", err)
			}
		}
		if _, err := store.addPlayer(room.ID); !errors.Is(err, ErrRoomFull) {
			t.Fatalf("expected ErrRoomFull, got %v", err)
		}
	})

	t.Run("room has no players", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()

		room, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		if _, err := store.startRoom(room.ID); !errors.Is(err, ErrRoomHasNoPlayers) {
			t.Fatalf("expected ErrRoomHasNoPlayers, got %v", err)
		}
	})

	t.Run("missing player", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()

		room, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		if _, err := store.reserveClient(room.ID, "missing", nil); !errors.Is(err, ErrPlayerNotFound) {
			t.Fatalf("expected ErrPlayerNotFound, got %v", err)
		}
	})

	t.Run("player already connected", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()

		room, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		player, err := store.addPlayer(room.ID)
		if err != nil {
			t.Fatalf("add player: %v", err)
		}
		if _, err := store.reserveClient(room.ID, player.Player.ID, []string{player.SessionToken}); err != nil {
			t.Fatalf("reserve first client: %v", err)
		}
		if _, err := store.reserveClient(room.ID, player.Player.ID, []string{player.SessionToken}); !errors.Is(err, ErrPlayerAlreadyConnected) {
			t.Fatalf("expected ErrPlayerAlreadyConnected, got %v", err)
		}
	})
}

func TestHandlerRouteContract(t *testing.T) {
	tests := []struct {
		name       string
		path       func(roomResponse, playerResponse) string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unknown root route",
			path:       func(roomResponse, playerResponse) string { return "/unknown" },
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "unknown nested room route",
			path:       func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID + "/unknown" },
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "missing player collection room",
			path:       func(roomResponse, playerResponse) string { return "/rooms/missing/players" },
			wantStatus: http.StatusMethodNotAllowed,
			wantCode:   "method_not_allowed",
		},
		{
			name:       "missing websocket room",
			path:       func(roomResponse, playerResponse) string { return "/rooms/missing/players/player-1" },
			wantStatus: http.StatusNotFound,
			wantCode:   "room_not_found",
		},
		{
			name:       "missing websocket player",
			path:       func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID + "/players/missing" },
			wantStatus: http.StatusNotFound,
			wantCode:   "player_not_found",
		},
		{
			name: "percent encoded room ID",
			path: func(room roomResponse, _ playerResponse) string {
				return "/rooms/" + strings.ReplaceAll(room.ID, "-", "%2D")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "encoded slash in room ID",
			path:       func(roomResponse, playerResponse) string { return "/rooms/room%2F1" },
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "encoded slash in websocket room ID",
			path:       func(roomResponse, playerResponse) string { return "/rooms/room%2F1/players/player-1" },
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "encoded slash in websocket player ID",
			path:       func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID + "/players/player%2F1" },
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "duplicate slash",
			path:       func(roomResponse, playerResponse) string { return "/rooms//" },
			wantStatus: http.StatusNotFound,
			wantCode:   "room_not_found",
		},
		{
			name:       "dot segment",
			path:       func(roomResponse, playerResponse) string { return "/rooms/./" },
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "dot dot segment",
			path:       func(roomResponse, playerResponse) string { return "/rooms/../rooms" },
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			handler := debugHandler(t, store)
			room := createRoom(t, handler)
			player := createPlayer(t, handler, room.ID)

			rec := request(handler, http.MethodGet, tt.path(room, player))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestHandlerRouteEncodedWildcardContract(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       func(roomResponse, playerResponse) string
		wantStatus int
		wantCode   string
	}{
		{
			name:   "escaped room detail get",
			method: http.MethodGet,
			path: func(room roomResponse, _ playerResponse) string {
				return "/rooms/" + strings.ReplaceAll(room.ID, "-", "%2D")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:   "escaped room detail delete",
			method: http.MethodDelete,
			path: func(room roomResponse, _ playerResponse) string {
				return "/rooms/" + strings.ReplaceAll(room.ID, "-", "%2D")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:   "escaped player collection",
			method: http.MethodPost,
			path: func(room roomResponse, _ playerResponse) string {
				return "/rooms/" + strings.ReplaceAll(room.ID, "-", "%2D") + "/players"
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:   "escaped start",
			method: http.MethodPost,
			path: func(room roomResponse, _ playerResponse) string {
				return "/rooms/" + strings.ReplaceAll(room.ID, "-", "%2D") + "/start"
			},
			wantStatus: http.StatusOK,
		},
		{name: "encoded slash room detail get", method: http.MethodGet, path: func(roomResponse, playerResponse) string { return "/rooms/room%2F1" }, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "encoded slash room detail delete", method: http.MethodDelete, path: func(roomResponse, playerResponse) string { return "/rooms/room%2F1" }, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "encoded slash player collection", method: http.MethodPost, path: func(roomResponse, playerResponse) string { return "/rooms/room%2F1/players" }, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "encoded slash start", method: http.MethodPost, path: func(roomResponse, playerResponse) string { return "/rooms/room%2F1/start" }, wantStatus: http.StatusNotFound, wantCode: "not_found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			handler := debugHandler(t, store)
			room := createRoom(t, handler)
			player := createPlayer(t, handler, room.ID)

			rec := request(handler, tt.method, tt.path(room, player))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestHandlerRouteDotSegmentDetailContract(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
	}{
		{name: "dot get", method: http.MethodGet, path: "/rooms/.", wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "dot delete", method: http.MethodDelete, path: "/rooms/.", wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "dot head", method: http.MethodHead, path: "/rooms/.", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "dot dot get", method: http.MethodGet, path: "/rooms/..", wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "dot dot delete", method: http.MethodDelete, path: "/rooms/..", wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "dot dot head", method: http.MethodHead, path: "/rooms/..", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "encoded dot get", method: http.MethodGet, path: "/rooms/%2e", wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "encoded dot head", method: http.MethodHead, path: "/rooms/%2e", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			rec := request(debugHandler(t, store), tt.method, tt.path)
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestHandlerRouteNestedDotSegmentContract(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       func(roomResponse) string
		wantStatus int
		wantCode   string
	}{
		{name: "raw dot room player collection", method: http.MethodPost, path: func(roomResponse) string { return "/rooms/./players" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "raw dot dot room player collection", method: http.MethodPost, path: func(roomResponse) string { return "/rooms/../players" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "encoded dot room player collection", method: http.MethodPost, path: func(roomResponse) string { return "/rooms/%2e/players" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "encoded dot dot room player collection", method: http.MethodPost, path: func(roomResponse) string { return "/rooms/%2e%2e/players" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "raw dot websocket room", method: http.MethodGet, path: func(roomResponse) string { return "/rooms/./players/player-1" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "raw dot dot websocket room", method: http.MethodGet, path: func(roomResponse) string { return "/rooms/../players/player-1" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "encoded dot websocket room", method: http.MethodGet, path: func(roomResponse) string { return "/rooms/%2e/players/player-1" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "encoded dot dot websocket room", method: http.MethodGet, path: func(roomResponse) string { return "/rooms/%2e%2e/players/player-1" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "raw dot websocket player", method: http.MethodGet, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/." }, wantStatus: http.StatusNotFound, wantCode: "player_not_found"},
		{name: "raw dot dot websocket player", method: http.MethodGet, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/.." }, wantStatus: http.StatusNotFound, wantCode: "player_not_found"},
		{name: "encoded dot websocket player", method: http.MethodGet, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/%2e" }, wantStatus: http.StatusNotFound, wantCode: "player_not_found"},
		{name: "encoded dot dot websocket player", method: http.MethodGet, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/%2e%2e" }, wantStatus: http.StatusNotFound, wantCode: "player_not_found"},
		{name: "raw dot websocket player head", method: http.MethodHead, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/." }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "raw dot dot websocket player head", method: http.MethodHead, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/.." }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "encoded dot websocket player head", method: http.MethodHead, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/%2e" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "encoded dot dot websocket player head", method: http.MethodHead, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/%2e%2e" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			handler := debugHandler(t, store)
			room := createRoom(t, handler)

			rec := request(handler, tt.method, tt.path(room))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
			if location := rec.Header().Get("Location"); location != "" {
				t.Fatalf("expected no redirect Location, got %q", location)
			}
		})
	}
}

func TestHandlerRouteEncodedSlashKnownRouteContract(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       func(roomResponse) string
		wantStatus int
		wantCode   string
	}{
		{name: "raw player collection get", method: http.MethodGet, path: func(roomResponse) string { return "/rooms/missing/players" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "encoded slash player collection get", method: http.MethodGet, path: func(roomResponse) string { return "/rooms/missing%2Fplayers" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "raw player collection post", method: http.MethodPost, path: func(roomResponse) string { return "/rooms/missing/players" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "encoded slash player collection post", method: http.MethodPost, path: func(roomResponse) string { return "/rooms/missing%2Fplayers" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "raw player collection head", method: http.MethodHead, path: func(roomResponse) string { return "/rooms/missing/players" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "encoded slash player collection head", method: http.MethodHead, path: func(roomResponse) string { return "/rooms/missing%2Fplayers" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "raw dot websocket player get", method: http.MethodGet, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/." }, wantStatus: http.StatusNotFound, wantCode: "player_not_found"},
		{name: "encoded slash dot websocket player get", method: http.MethodGet, path: func(room roomResponse) string { return "/rooms/" + room.ID + "%2Fplayers%2F%2e" }, wantStatus: http.StatusNotFound, wantCode: "player_not_found"},
		{name: "raw dot websocket player head", method: http.MethodHead, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/." }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "encoded slash dot websocket player head", method: http.MethodHead, path: func(room roomResponse) string { return "/rooms/" + room.ID + "%2Fplayers%2F%2e" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "encoded slash room prefix get", method: http.MethodGet, path: func(roomResponse) string { return "/rooms%2Fmissing" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "encoded slash room prefix head", method: http.MethodHead, path: func(roomResponse) string { return "/rooms%2Fmissing" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			handler := debugHandler(t, store)
			room := createRoom(t, handler)

			rec := request(handler, tt.method, tt.path(room))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
			if location := rec.Header().Get("Location"); location != "" {
				t.Fatalf("expected no redirect Location, got %q", location)
			}
		})
	}
}

func TestHandlerRoutePatternsPopulatePathValues(t *testing.T) {
	store := NewStore(5)
	defer store.Close()
	room, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	player, err := store.addPlayer(room.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	router := newRouter(store)

	tests := []struct {
		name         string
		method       string
		path         string
		wantStatus   int
		wantPattern  string
		wantRoomID   string
		wantPlayerID string
	}{
		{name: "room collection", method: http.MethodGet, path: "/rooms", wantStatus: http.StatusOK, wantPattern: "GET /rooms"},
		{name: "room detail", method: http.MethodGet, path: "/rooms/" + room.ID, wantStatus: http.StatusOK, wantPattern: "GET /rooms/{roomID}", wantRoomID: room.ID},
		{name: "player collection", method: http.MethodPost, path: "/rooms/" + room.ID + "/players", wantStatus: http.StatusCreated, wantPattern: "POST /rooms/{roomID}/players", wantRoomID: room.ID},
		{name: "start", method: http.MethodPost, path: "/rooms/" + room.ID + "/start", wantStatus: http.StatusOK, wantPattern: "POST /rooms/{roomID}/start", wantRoomID: room.ID},
		{name: "websocket head", method: http.MethodHead, path: "/rooms/" + room.ID + "/players/" + player.Player.ID, wantStatus: http.StatusMethodNotAllowed, wantPattern: "HEAD /rooms/{roomID}/players/{playerID}", wantRoomID: room.ID, wantPlayerID: player.Player.ID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}
			if req.Pattern != tt.wantPattern {
				t.Fatalf("expected pattern %q, got %q", tt.wantPattern, req.Pattern)
			}
			if got := req.PathValue("roomID"); got != tt.wantRoomID {
				t.Fatalf("expected room path value %q, got %q", tt.wantRoomID, got)
			}
			if got := req.PathValue("playerID"); got != tt.wantPlayerID {
				t.Fatalf("expected player path value %q, got %q", tt.wantPlayerID, got)
			}
		})
	}
}

func TestHandlerMethodContract(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       func(roomResponse, playerResponse) string
		wantStatus int
		wantCode   string
	}{
		{name: "get room collection", method: http.MethodGet, path: func(roomResponse, playerResponse) string { return "/rooms" }, wantStatus: http.StatusOK},
		{name: "post room collection", method: http.MethodPost, path: func(roomResponse, playerResponse) string { return "/rooms" }, wantStatus: http.StatusCreated},
		{name: "delete room collection", method: http.MethodDelete, path: func(roomResponse, playerResponse) string { return "/rooms" }, wantStatus: http.StatusOK},
		{name: "put matchmaking", method: http.MethodPut, path: func(roomResponse, playerResponse) string { return "/matchmaking/join" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "put room collection", method: http.MethodPut, path: func(roomResponse, playerResponse) string { return "/rooms" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "put room detail", method: http.MethodPut, path: func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "put missing room detail", method: http.MethodPut, path: func(roomResponse, playerResponse) string { return "/rooms/missing" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "put player collection", method: http.MethodPut, path: func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID + "/players" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "get missing player collection", method: http.MethodGet, path: func(roomResponse, playerResponse) string { return "/rooms/missing/players" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "put start", method: http.MethodPut, path: func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID + "/start" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "get missing start", method: http.MethodGet, path: func(roomResponse, playerResponse) string { return "/rooms/missing/start" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "put websocket path", method: http.MethodPut, path: func(room roomResponse, player playerResponse) string {
			return "/rooms/" + room.ID + "/players/" + player.ID
		}, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "put missing websocket path", method: http.MethodPut, path: func(roomResponse, playerResponse) string {
			return "/rooms/missing/players/missing"
		}, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			handler := debugHandler(t, store)
			room := createRoom(t, handler)
			player := createPlayer(t, handler, room.ID)

			rec := request(handler, tt.method, tt.path(room, player))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestHandlerRouteErrorContract(t *testing.T) {
	t.Run("room cap from collection", func(t *testing.T) {
		store := NewStore(1)
		defer store.Close()
		handler := debugHandler(t, store)
		_ = createRoom(t, handler)

		rec := request(handler, http.MethodPost, "/rooms")
		assertJSONRouteResponse(t, rec, http.StatusConflict, "room_cap_reached")
	})

	t.Run("room cap from matchmaking", func(t *testing.T) {
		store := NewStore(1)
		defer store.Close()
		handler := debugHandler(t, store)
		room := createRoom(t, handler)
		for range store.matchCapacity() {
			_ = createPlayer(t, handler, room.ID)
		}

		rec := request(handler, http.MethodPost, "/matchmaking/join")
		assertJSONRouteResponse(t, rec, http.StatusConflict, "room_cap_reached")
	})

	t.Run("room full", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()
		handler := debugHandler(t, store)
		room := createRoom(t, handler)
		for range store.debugRoomCapacity() {
			_ = createPlayer(t, handler, room.ID)
		}

		rec := request(handler, http.MethodPost, "/rooms/"+room.ID+"/players")
		assertJSONRouteResponse(t, rec, http.StatusConflict, "room_full")
	})

	t.Run("room has no players", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()
		handler := debugHandler(t, store)
		room := createRoom(t, handler)

		rec := request(handler, http.MethodPost, "/rooms/"+room.ID+"/start")
		assertJSONRouteResponse(t, rec, http.StatusConflict, "room_has_no_players")
	})

	t.Run("room not found from player collection", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()
		handler := debugHandler(t, store)
		rec := request(handler, http.MethodPost, "/rooms/missing/players")
		assertJSONRouteResponse(t, rec, http.StatusNotFound, "room_not_found")
	})

	t.Run("room not found from start", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()
		handler := debugHandler(t, store)
		rec := request(handler, http.MethodPost, "/rooms/missing/start")
		assertJSONRouteResponse(t, rec, http.StatusNotFound, "room_not_found")
	})

	t.Run("room detail not found", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()
		handler := debugHandler(t, store)
		rec := request(handler, http.MethodGet, "/rooms/missing")
		assertJSONRouteResponse(t, rec, http.StatusNotFound, "room_not_found")
	})

	t.Run("room not found before websocket upgrade", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()
		handler := debugHandler(t, store)
		rec := request(handler, http.MethodGet, "/rooms/missing/players/player-1")
		assertJSONRouteResponse(t, rec, http.StatusNotFound, "room_not_found")
	})

	t.Run("player not found before websocket upgrade", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()
		handler := debugHandler(t, store)
		room := createRoom(t, handler)

		rec := request(handler, http.MethodGet, "/rooms/"+room.ID+"/players/missing")
		assertJSONRouteResponse(t, rec, http.StatusNotFound, "player_not_found")
	})
}

func TestHandlerTrailingSlashContract(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       func(roomResponse) string
		wantStatus int
		wantCode   string
	}{
		{name: "get room collection slash", method: http.MethodGet, path: func(roomResponse) string { return "/rooms/" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "post room collection slash", method: http.MethodPost, path: func(roomResponse) string { return "/rooms/" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "delete room collection slash", method: http.MethodDelete, path: func(roomResponse) string { return "/rooms/" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "room detail slash", method: http.MethodGet, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/" }, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "player collection slash", method: http.MethodPost, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "start slash", method: http.MethodPost, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/start/" }, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "websocket empty player slash", method: http.MethodGet, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/" }, wantStatus: http.StatusNotFound, wantCode: "player_not_found"},
		{name: "websocket path slash", method: http.MethodGet, path: func(room roomResponse) string { return "/rooms/" + room.ID + "/players/player-1/" }, wantStatus: http.StatusNotFound, wantCode: "not_found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			handler := debugHandler(t, store)
			room := createRoom(t, handler)

			rec := request(handler, tt.method, tt.path(room))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestHandlerHeadContract(t *testing.T) {
	tests := []struct {
		name       string
		path       func(roomResponse, playerResponse) string
		wantStatus int
		wantCode   string
	}{
		{name: "matchmaking", path: func(roomResponse, playerResponse) string { return "/matchmaking/join" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "room collection", path: func(roomResponse, playerResponse) string { return "/rooms" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "room collection slash", path: func(roomResponse, playerResponse) string { return "/rooms/" }, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "room detail", path: func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "player collection", path: func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID + "/players" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "start", path: func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID + "/start" }, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "websocket path", path: func(room roomResponse, player playerResponse) string {
			return "/rooms/" + room.ID + "/players/" + player.ID
		}, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "unknown", path: func(roomResponse, playerResponse) string { return "/unknown" }, wantStatus: http.StatusNotFound, wantCode: "not_found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			handler := debugHandler(t, store)
			room := createRoom(t, handler)
			player := createPlayer(t, handler, room.ID)

			rec := request(handler, http.MethodHead, tt.path(room, player))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestHandlerListsAndCreatesRooms(t *testing.T) {
	handler := debugHandler(t, NewStore(5))

	listRec := request(handler, http.MethodGet, "/rooms")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected initial list status 200, got %d", listRec.Code)
	}
	var initial roomListResponse
	decodeResponse(t, listRec, &initial)
	if len(initial.Rooms) != 0 {
		t.Fatalf("expected no rooms, got %d", len(initial.Rooms))
	}

	createRec := request(handler, http.MethodPost, "/rooms")
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d", createRec.Code)
	}
	var created roomResponse
	decodeResponse(t, createRec, &created)
	if created.ID == "" {
		t.Fatal("expected room ID to be assigned")
	}
	if created.Status != RoomStatusWaiting {
		t.Fatalf("expected waiting status, got %q", created.Status)
	}
	if created.LatestSnapshot.Tick != 0 || created.LatestSnapshot.PlayerCount != 0 {
		t.Fatalf("unexpected snapshot summary: %+v", created.LatestSnapshot)
	}

	listRec = request(handler, http.MethodGet, "/rooms")
	var afterCreate roomListResponse
	decodeResponse(t, listRec, &afterCreate)
	if len(afterCreate.Rooms) != 1 || afterCreate.Rooms[0].ID != created.ID {
		t.Fatalf("expected created room in list, got %+v", afterCreate.Rooms)
	}
}

func TestHandlerReturnsRoomDetailWithLatestSnapshotSummary(t *testing.T) {
	handler := debugHandler(t, NewStore(5))

	createRec := request(handler, http.MethodPost, "/rooms")
	var created roomResponse
	decodeResponse(t, createRec, &created)

	detailRec := request(handler, http.MethodGet, "/rooms/"+created.ID)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("expected detail status 200, got %d", detailRec.Code)
	}
	var detail roomResponse
	decodeResponse(t, detailRec, &detail)
	if detail.ID != created.ID {
		t.Fatalf("expected room ID %q, got %q", created.ID, detail.ID)
	}
	if detail.LatestSnapshot.Tick != 0 {
		t.Fatalf("expected tick 0 summary, got %d", detail.LatestSnapshot.Tick)
	}
}

func TestHandlerRoomDetailShowsLatestSnapshotSummaryAfterTicks(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	defer store.Close()

	room := createRoom(t, handler)
	_ = createPlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	store.tickRoom(room.ID)

	detailRec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("expected detail status 200, got %d", detailRec.Code)
	}
	var detail roomResponse
	decodeResponse(t, detailRec, &detail)
	if detail.LatestSnapshot.Tick != 1 {
		t.Fatalf("expected latest snapshot tick 1, got %d", detail.LatestSnapshot.Tick)
	}
	if detail.LatestSnapshot.PlayerCount != 1 {
		t.Fatalf("expected latest snapshot player count 1, got %+v", detail.LatestSnapshot)
	}
}

func TestHandlerRejectsRoomCreationAtCap(t *testing.T) {
	handler := debugHandler(t, NewStore(5))

	for i := 0; i < 5; i++ {
		rec := request(handler, http.MethodPost, "/rooms")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected room %d create status 201, got %d", i+1, rec.Code)
		}
	}

	rec := request(handler, http.MethodPost, "/rooms")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected cap status 409, got %d", rec.Code)
	}
	assertError(t, rec, "room_cap_reached")
}

func TestHandlerClearsRoomsForDebugCapRecovery(t *testing.T) {
	handler := debugHandler(t, NewStore(5))

	for i := 0; i < 5; i++ {
		rec := request(handler, http.MethodPost, "/rooms")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected room %d create status 201, got %d", i+1, rec.Code)
		}
	}
	if rec := request(handler, http.MethodPost, "/rooms"); rec.Code != http.StatusConflict {
		t.Fatalf("expected cap status 409 before clear, got %d", rec.Code)
	}

	clearRec := request(handler, http.MethodDelete, "/rooms")
	if clearRec.Code != http.StatusOK {
		t.Fatalf("expected clear status 200, got %d", clearRec.Code)
	}
	var cleared clearRoomsResponse
	decodeResponse(t, clearRec, &cleared)
	if cleared.Deleted != 5 {
		t.Fatalf("expected clear to delete 5 rooms, got %d", cleared.Deleted)
	}

	listRec := request(handler, http.MethodGet, "/rooms")
	var list roomListResponse
	decodeResponse(t, listRec, &list)
	if len(list.Rooms) != 0 {
		t.Fatalf("expected empty room list after clear, got %+v", list.Rooms)
	}

	createRec := request(handler, http.MethodPost, "/rooms")
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected room creation after clear to recover, got %d", createRec.Code)
	}
}

func TestStoreDeleteRoomIfSamePreservesReplacement(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	original := store.lookupRoom(created.ID)

	store.mu.Lock()
	replacement := store.newRoomLocked(created.ID, store.gameConfig)
	store.rooms[created.ID] = replacement
	store.mu.Unlock()

	if store.deleteRoomIfSame(created.ID, original) {
		t.Fatal("expected stale pointer not to delete its replacement")
	}
	if got := store.lookupRoom(created.ID); got != replacement {
		t.Fatal("expected replacement room to remain registered")
	}
}

func TestStoreJanitorCleanupDoesNotDeleteReplacementFromStaleSnapshot(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create waiting room: %v", err)
	}
	original := store.lookupRoom(created.ID)
	fakeClock.Advance(defaultWaitingRoomIdleTTL)

	const staleSnapshotKey = "stale-snapshot-entry"
	store.mu.Lock()
	replacement := store.newRoomLocked(created.ID, store.gameConfig)
	store.rooms[created.ID] = replacement
	store.rooms[staleSnapshotKey] = original
	store.mu.Unlock()
	t.Cleanup(func() {
		store.mu.Lock()
		delete(store.rooms, staleSnapshotKey)
		store.mu.Unlock()
	})

	if deleted := store.cleanupExpired(fakeClock.Now()); deleted != 0 {
		t.Fatalf("expected stale cleanup not to delete a replacement, got %d deletions", deleted)
	}
	if got := store.lookupRoom(created.ID); got != replacement {
		t.Fatal("expected replacement room to remain registered")
	}
	replacement.mu.Lock()
	replacementRemoved := replacement.removed
	replacement.mu.Unlock()
	if replacementRemoved {
		t.Fatal("expected stale cleanup not to mark replacement removed")
	}
}

func TestStoreStartsExactlyOneThirtySecondJanitor(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)

	if got := fakeClock.TickerCount(30 * time.Second); got != 1 {
		t.Fatalf("expected exactly one 30s janitor ticker, got %d", got)
	}
	if got := fakeClock.TotalTickerCount(); got != 1 {
		t.Fatalf("expected janitor to be the only constructor ticker, got %d tickers", got)
	}
}

func TestStoreCloseStopsWaitsForJanitorAndIsIdempotent(t *testing.T) {
	clock := newBlockingStopClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(func() {
		clock.ticker.release()
		store.Close()
	})

	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	go func() {
		store.Close()
		close(firstDone)
	}()
	go func() {
		store.Close()
		close(secondDone)
	}()

	select {
	case <-clock.ticker.stopStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected Close to stop the janitor ticker")
	}
	for name, done := range map[string]<-chan struct{}{"first": firstDone, "second": secondDone} {
		select {
		case <-done:
			t.Fatalf("expected %s Close call to wait for janitor shutdown", name)
		default:
		}
	}

	clock.ticker.release()
	for name, done := range map[string]<-chan struct{}{"first": firstDone, "second": secondDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("expected %s Close call to finish after janitor shutdown", name)
		}
	}

	store.Close()
	if got := clock.ticker.stops(); got != 1 {
		t.Fatalf("expected idempotent Close to stop janitor once, got %d", got)
	}
}

func TestStoreJanitorSweepsAllExpiredRooms(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)

	for range 3 {
		if _, err := store.createRoom(); err != nil {
			t.Fatalf("create waiting room: %v", err)
		}
	}
	fakeClock.Advance(defaultWaitingRoomIdleTTL)
	fakeClock.TickTicker(30*time.Second, 0)

	waitForStoreRoomCount(t, store, 0)
}

func TestStoreTickRoomDoesNotSweepExpiredRooms(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)

	started := createStartedRoomInStore(t, store)
	keepRoomActiveForCleanupTest(store.lookupRoom(started.ID))
	expired, err := store.createRoom()
	if err != nil {
		t.Fatalf("create waiting room: %v", err)
	}
	fakeClock.Advance(defaultWaitingRoomIdleTTL)

	store.tickRoom(started.ID)

	if store.lookupRoom(expired.ID) == nil {
		t.Fatal("expected tickRoom not to scan or clean another room")
	}
}

func TestStoreRunRoomDoesNotSweepExpiredRooms(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)

	started := createStartedRoomInStore(t, store)
	startedRoom := store.lookupRoom(started.ID)
	keepRoomActiveForCleanupTest(startedRoom)
	stepped := make(chan struct{}, 1)
	startedRoom.mu.Lock()
	originalStepper := startedRoom.state
	startedRoom.state = testRoomStepper(func(inputs []simulation.InputCommand) simulation.Snapshot {
		stepped <- struct{}{}
		return originalStepper.Step(inputs)
	})
	startedRoom.mu.Unlock()

	expired, err := store.createRoom()
	if err != nil {
		t.Fatalf("create waiting room: %v", err)
	}
	fakeClock.Advance(defaultWaitingRoomIdleTTL)
	fakeClock.TickTicker(time.Second/time.Duration(store.gameConfig.TickRate), 0)

	select {
	case <-stepped:
	case <-time.After(time.Second):
		t.Fatal("expected runRoom to process the gameplay ticker")
	}
	if store.lookupRoom(expired.ID) == nil {
		t.Fatal("expected runRoom not to scan or clean another room")
	}
}

func TestStoreJanitorClosesExpiredResourcesOutsideStoreAndRoomLocks(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create waiting room: %v", err)
	}
	resourceTicker := newBlockingStopTicker()
	t.Cleanup(resourceTicker.release)
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.ticker = resourceTicker
	room.mu.Unlock()

	fakeClock.Advance(defaultWaitingRoomIdleTTL)
	fakeClock.TickTicker(30*time.Second, 0)
	select {
	case <-resourceTicker.stopStarted:
	case <-time.After(time.Second):
		t.Fatal("expected janitor to close expired room resources")
	}

	assertCompletes(t, "Store lock while resource close blocks", func() {
		store.mu.Lock()
		store.mu.Unlock()
	})
	assertCompletes(t, "room lock while resource close blocks", func() {
		room.mu.Lock()
		room.mu.Unlock()
	})
	if store.lookupRoom(created.ID) != nil {
		t.Fatal("expected expired room to be deleted before resource close")
	}

	resourceTicker.release()
	if got := resourceTicker.stops(); got != 1 {
		t.Fatalf("expected expired room ticker to stop once, got %d", got)
	}
}

func TestStoreDebugCreateAtCapCleansUpAndRetriesExactlyOnce(t *testing.T) {
	t.Run("expired room is reclaimed", func(t *testing.T) {
		clock := newCountingNowClock()
		store := NewStoreWithClock(1, clock)
		t.Cleanup(store.Close)
		handler := debugHandler(t, store)

		expired := createRoom(t, handler)
		clock.Advance(defaultWaitingRoomIdleTTL)
		clock.ResetNowCalls()

		rec := request(handler, http.MethodPost, "/rooms")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected cap cleanup retry to create a room, got status %d", rec.Code)
		}
		if store.lookupRoom(expired.ID) != nil {
			t.Fatal("expected cap cleanup to remove the expired room")
		}
		if got := clock.NowCalls(); got != 2 {
			t.Fatalf("expected one cleanup time read and one retried room creation, got %d time reads", got)
		}
	})

	t.Run("non-expired cap remains conflict", func(t *testing.T) {
		clock := newCountingNowClock()
		store := NewStoreWithClock(1, clock)
		t.Cleanup(store.Close)
		handler := debugHandler(t, store)

		original := createRoom(t, handler)
		clock.ResetNowCalls()

		rec := request(handler, http.MethodPost, "/rooms")
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected non-expired cap status 409, got %d", rec.Code)
		}
		assertError(t, rec, "room_cap_reached")
		if store.lookupRoom(original.ID) == nil {
			t.Fatal("expected non-expired capped room to remain")
		}
		if got := clock.NowCalls(); got != 1 {
			t.Fatalf("expected exactly one cap cleanup attempt, got %d time reads", got)
		}
	})
}

func TestStoreMatchmakingAtCapCleansUpAndRetriesExactlyOnce(t *testing.T) {
	t.Run("expired room is reclaimed", func(t *testing.T) {
		clock := newCountingNowClock()
		store := NewStoreWithClock(1, clock)
		t.Cleanup(store.Close)
		handler := debugHandler(t, store)

		expired := createRoom(t, handler)
		for range store.matchCapacity() {
			_ = createPlayer(t, handler, expired.ID)
		}
		clock.Advance(defaultWaitingRoomIdleTTL)
		clock.ResetNowCalls()

		rec := request(handler, http.MethodPost, "/matchmaking/join")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected matchmaking cap cleanup retry to create a room, got status %d", rec.Code)
		}
		if store.lookupRoom(expired.ID) != nil {
			t.Fatal("expected matchmaking cap cleanup to remove the expired room")
		}
		if got := clock.NowCalls(); got != 3 {
			t.Fatalf("expected one cleanup read plus retried room and player activity reads, got %d", got)
		}
	})

	t.Run("non-expired cap remains conflict", func(t *testing.T) {
		clock := newCountingNowClock()
		store := NewStoreWithClock(1, clock)
		t.Cleanup(store.Close)
		handler := debugHandler(t, store)

		original := createRoom(t, handler)
		for range store.matchCapacity() {
			_ = createPlayer(t, handler, original.ID)
		}
		clock.ResetNowCalls()

		rec := request(handler, http.MethodPost, "/matchmaking/join")
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected non-expired matchmaking cap status 409, got %d", rec.Code)
		}
		assertError(t, rec, "room_cap_reached")
		if store.lookupRoom(original.ID) == nil {
			t.Fatal("expected non-expired capped room to remain")
		}
		if got := clock.NowCalls(); got != 1 {
			t.Fatalf("expected exactly one matchmaking cap cleanup attempt, got %d time reads", got)
		}
	})
}

func TestStoreBelowCapDoesNotCleanExpiredRooms(t *testing.T) {
	t.Run("debug create", func(t *testing.T) {
		clock := newCountingNowClock()
		store := NewStoreWithClock(2, clock)
		t.Cleanup(store.Close)

		expired, err := store.createRoom()
		if err != nil {
			t.Fatalf("create waiting room: %v", err)
		}
		clock.Advance(defaultWaitingRoomIdleTTL)
		clock.ResetNowCalls()

		if _, err := store.createRoom(); err != nil {
			t.Fatalf("create room below cap: %v", err)
		}
		if store.lookupRoom(expired.ID) == nil {
			t.Fatal("expected below-cap debug create not to clean the expired room")
		}
		if got := clock.NowCalls(); got != 1 {
			t.Fatalf("expected only the new room creation time read, got %d", got)
		}
	})

	t.Run("matchmaking join", func(t *testing.T) {
		clock := newCountingNowClock()
		store := NewStoreWithClock(2, clock)
		t.Cleanup(store.Close)

		expired, err := store.createRoom()
		if err != nil {
			t.Fatalf("create waiting room: %v", err)
		}
		clock.Advance(defaultWaitingRoomIdleTTL)
		clock.ResetNowCalls()

		joined, err := store.joinMatchmaking(store.defaultGameMode())
		if err != nil {
			t.Fatalf("join matchmaking below cap: %v", err)
		}
		if joined.Room.ID != expired.ID {
			t.Fatalf("expected below-cap matchmaking to use the registered room, got %q", joined.Room.ID)
		}
		if got := clock.NowCalls(); got != 1 {
			t.Fatalf("expected only joined player activity time read, got %d", got)
		}
	})
}

func keepRoomActiveForCleanupTest(room *room) {
	room.mu.Lock()
	room.clients["cleanup-test-client"] = nil
	room.disconnectedAt = time.Time{}
	room.mu.Unlock()
}

func waitForStoreRoomCount(t *testing.T, store *Store, want int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.RLock()
		got := len(store.rooms)
		store.mu.RUnlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}

	store.mu.RLock()
	got := len(store.rooms)
	store.mu.RUnlock()
	t.Fatalf("expected %d registered rooms, got %d", want, got)
}

func assertCompletes(t *testing.T, name string, operation func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		operation()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("expected %s to complete", name)
	}
}

type blockingStopClock struct {
	now    time.Time
	ticker *blockingStopTicker
}

type countingNowClock struct {
	clock *fakeClock
	mu    sync.Mutex
	calls int
}

func newCountingNowClock() *countingNowClock {
	return &countingNowClock{clock: newFakeClock()}
}

func (c *countingNowClock) Now() time.Time {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.clock.Now()
}

func (c *countingNowClock) NewTicker(duration time.Duration) ticker {
	return c.clock.NewTicker(duration)
}

func (c *countingNowClock) Advance(duration time.Duration) {
	c.clock.Advance(duration)
}

func (c *countingNowClock) ResetNowCalls() {
	c.mu.Lock()
	c.calls = 0
	c.mu.Unlock()
}

func (c *countingNowClock) NowCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newBlockingStopClock() *blockingStopClock {
	return &blockingStopClock{
		now:    time.Date(2026, 5, 30, 7, 0, 0, 0, time.UTC),
		ticker: newBlockingStopTicker(),
	}
}

func (c *blockingStopClock) Now() time.Time {
	return c.now
}

func (c *blockingStopClock) NewTicker(time.Duration) ticker {
	return c.ticker
}

type blockingStopTicker struct {
	ticks       chan time.Time
	stopStarted chan struct{}
	releaseStop chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	stopCount   int
}

func newBlockingStopTicker() *blockingStopTicker {
	return &blockingStopTicker{
		ticks:       make(chan time.Time),
		stopStarted: make(chan struct{}),
		releaseStop: make(chan struct{}),
	}
}

func (t *blockingStopTicker) C() <-chan time.Time {
	return t.ticks
}

func (t *blockingStopTicker) Stop() {
	t.mu.Lock()
	t.stopCount++
	t.mu.Unlock()
	t.startOnce.Do(func() { close(t.stopStarted) })
	<-t.releaseStop
}

func (t *blockingStopTicker) release() {
	t.releaseOnce.Do(func() { close(t.releaseStop) })
}

func (t *blockingStopTicker) stops() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopCount
}

func TestStoreDeletedRoomReleasesPlayerIDForReuse(t *testing.T) {
	firstRoomID := bytes.Repeat([]byte{0x61}, 16)
	playerID := bytes.Repeat([]byte{0x62}, 16)
	firstToken := bytes.Repeat([]byte{0x63}, 32)
	secondRoomID := bytes.Repeat([]byte{0x64}, 16)
	secondToken := bytes.Repeat([]byte{0x65}, 32)
	random := bytes.NewReader(bytes.Join([][]byte{
		firstRoomID,
		playerID,
		firstToken,
		secondRoomID,
		playerID,
		secondToken,
	}, nil))
	store := NewStoreWithConfig(5, StoreConfig{Random: random})
	t.Cleanup(store.Close)

	firstRoom, err := store.createRoom()
	if err != nil {
		t.Fatalf("create first room: %v", err)
	}
	firstPlayer, err := store.addPlayer(firstRoom.ID)
	if err != nil {
		t.Fatalf("add first player: %v", err)
	}
	if _, ok := store.deleteRoom(firstRoom.ID); !ok {
		t.Fatal("delete first room")
	}

	secondRoom, err := store.createRoom()
	if err != nil {
		t.Fatalf("create second room: %v", err)
	}
	secondPlayer, err := store.addPlayer(secondRoom.ID)
	if err != nil {
		t.Fatalf("add second player: %v", err)
	}
	if secondPlayer.Player.ID != firstPlayer.Player.ID {
		t.Fatalf("expected deleted room player ID to be reusable, got %q then %q", firstPlayer.Player.ID, secondPlayer.Player.ID)
	}
}

func TestStoreRemovedRoomRejectsNewStateAccess(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}

	removed := store.lookupRoom(created.ID)
	var resources roomResources
	removed.mu.Lock()
	playerIDs, markedRemoved := resources.removeRoomLocked(removed)
	if !markedRemoved {
		removed.mu.Unlock()
		t.Fatal("mark room removed")
	}
	removed.mu.Unlock()
	defer func() {
		if store.deleteRoomIfSame(created.ID, removed) {
			store.releasePlayerIDs(playerIDs)
		}
		resources.close(defaultRoomDebugDeleteMsg)
	}()

	if got := store.listRooms().Rooms; len(got) != 0 {
		t.Fatalf("expected removed room to be omitted from list, got %+v", got)
	}
	if _, err := store.addPlayer(created.ID); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("expected removed room add to fail with ErrRoomNotFound, got %v", err)
	}
	store.setInput(created.ID, issued.Player.ID, inputMessage{MoveDir: simulation.Vector2{X: 1}}, nil)
	removed.mu.Lock()
	_, hasPendingInput := removed.pendingInputs[issued.Player.ID]
	removed.mu.Unlock()
	if hasPendingInput {
		t.Fatal("expected removed room not to accept input")
	}
	if _, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken}); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("expected removed room reservation to fail with ErrRoomNotFound, got %v", err)
	}
	if store.attachClient(reservation, nil) {
		t.Fatal("expected removed room reservation not to attach")
	}
	joined, err := store.joinMatchmaking(store.defaultGameMode())
	if err != nil {
		t.Fatalf("join matchmaking after removed room: %v", err)
	}
	if joined.Room.ID == created.ID {
		t.Fatal("expected matchmaking not to re-enter removed room")
	}
}

func TestHandlerDeletesSingleRoomAndStopsResources(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	defer store.Close()

	room := createRoom(t, handler)
	_ = createPlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	deleteRec := request(handler, http.MethodDelete, "/rooms/"+room.ID)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected delete status 200, got %d", deleteRec.Code)
	}
	var deleted clearRoomsResponse
	decodeResponse(t, deleteRec, &deleted)
	if deleted.Deleted != 1 {
		t.Fatalf("expected one deleted room, got %d", deleted.Deleted)
	}
	if fakeClock.StopCount() != 1 {
		t.Fatalf("expected room ticker to stop once, got %d", fakeClock.StopCount())
	}

	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected deleted room status 404, got %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func TestMatchmakingJoinGameMode(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantMode   string
		wantCode   string
	}{
		{name: "no body defaults", body: "", wantStatus: http.StatusCreated, wantMode: simulation.GameModeDuel1v1},
		{name: "empty object defaults", body: `{}`, wantStatus: http.StatusCreated, wantMode: simulation.GameModeDuel1v1},
		{name: "empty mode defaults", body: `{"gameMode":""}`, wantStatus: http.StatusCreated, wantMode: simulation.GameModeDuel1v1},
		{name: "solo", body: `{"gameMode":"solo"}`, wantStatus: http.StatusCreated, wantMode: simulation.GameModeSolo},
		{name: "team", body: `{"gameMode":"team"}`, wantStatus: http.StatusCreated, wantMode: simulation.GameModeTeam},
		{name: "trailing whitespace", body: "{\"gameMode\":\"solo\"} \n\t", wantStatus: http.StatusCreated, wantMode: simulation.GameModeSolo},
		{name: "unknown", body: `{"gameMode":"ranked"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_game_mode"},
		{name: "whitespace mode", body: `{"gameMode":" "}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_game_mode"},
		{name: "top-level null", body: `null`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "null mode", body: `{"gameMode":null}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "malformed", body: `{"gameMode":`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "trailing JSON", body: `{"gameMode":"solo"} {}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			handler := debugHandler(t, store)

			rec := requestWithBody(handler, http.MethodPost, "/matchmaking/join", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
			if tt.wantCode != "" {
				assertError(t, rec, tt.wantCode)
				if got := len(store.listRooms().Rooms); got != 0 {
					t.Fatalf("expected rejected join not to create a room, got %d", got)
				}
				store.mu.RLock()
				playerIDCount := len(store.playerIDs)
				store.mu.RUnlock()
				if playerIDCount != 0 {
					t.Fatalf("expected rejected join not to create player state, got %d player IDs", playerIDCount)
				}
				return
			}

			var response matchmakingJoinResponse
			decodeResponse(t, rec, &response)
			if response.GameMode != tt.wantMode || response.Room.GameMode != tt.wantMode {
				t.Fatalf("expected top-level and room mode %q, got %+v", tt.wantMode, response)
			}
			if response.GameMode != response.Room.GameMode {
				t.Fatalf("expected matching response modes, got %q and %q", response.GameMode, response.Room.GameMode)
			}
			if response.Room.MaxPlayers != simulation.StaticMapFixture().MaxPlayers {
				t.Fatalf("expected map/debug capacity %d, got %d", simulation.StaticMapFixture().MaxPlayers, response.Room.MaxPlayers)
			}

			stored := store.lookupRoom(response.Room.ID)
			if stored == nil {
				t.Fatalf("expected room %q in store", response.Room.ID)
			}
			stored.mu.Lock()
			selectedMode := stored.gameConfig.SelectedMode.ID
			stored.mu.Unlock()
			if selectedMode != tt.wantMode {
				t.Fatalf("expected room-owned selected mode %q, got %q", tt.wantMode, selectedMode)
			}
		})
	}
}

func TestMatchmakingJoinSeparatesModePools(t *testing.T) {
	store := NewStore(5)
	handler := debugHandler(t, store)

	duel := joinMatchmakingWithMode(t, handler, simulation.GameModeDuel1v1)
	firstSolo := joinMatchmakingWithMode(t, handler, simulation.GameModeSolo)
	team := joinMatchmakingWithMode(t, handler, simulation.GameModeTeam)
	secondSolo := joinMatchmakingWithMode(t, handler, simulation.GameModeSolo)
	thirdSolo := joinMatchmakingWithMode(t, handler, simulation.GameModeSolo)

	if duel.Room.ID == firstSolo.Room.ID || duel.Room.ID == team.Room.ID || firstSolo.Room.ID == team.Room.ID {
		t.Fatalf("expected duel, solo, and team to use different rooms, got %q, %q, and %q", duel.Room.ID, firstSolo.Room.ID, team.Room.ID)
	}
	if secondSolo.Room.ID != firstSolo.Room.ID {
		t.Fatalf("expected solo joins to reuse room %q, got %q", firstSolo.Room.ID, secondSolo.Room.ID)
	}
	if len(secondSolo.Room.Players) != 2 {
		t.Fatalf("expected reused solo room to contain two players, got %+v", secondSolo.Room.Players)
	}
	if thirdSolo.Room.ID != firstSolo.Room.ID || len(thirdSolo.Room.Players) != 3 {
		t.Fatalf("expected solo room %q to use mode capacity beyond duel size, got %+v", firstSolo.Room.ID, thirdSolo.Room)
	}
	wantSoloTeams := []string{"solo-1", "solo-2", "solo-3"}
	for index, player := range thirdSolo.Room.Players {
		if player.Team != wantSoloTeams[index] || player.Slot != 0 {
			t.Fatalf("expected solo player %d assignment %s slot 0, got %+v", index, wantSoloTeams[index], player)
		}
	}
}

func TestConcurrentMatchmakingJoinsReuseSingleModeRoom(t *testing.T) {
	const playerCount = 6
	for attempt := 0; attempt < 25; attempt++ {
		store := NewStore(10)
		start := make(chan struct{})
		responses := make([]matchmakingJoinResponse, playerCount)
		errs := make([]error, playerCount)
		var workers sync.WaitGroup
		workers.Add(playerCount)
		for index := 0; index < playerCount; index++ {
			go func() {
				defer workers.Done()
				<-start
				responses[index], errs[index] = store.joinMatchmaking(simulation.GameModeSolo)
			}()
		}

		close(start)
		workers.Wait()
		for index, err := range errs {
			if err != nil {
				store.Close()
				t.Fatalf("attempt %d join %d: %v", attempt, index, err)
			}
		}

		roomID := responses[0].Room.ID
		for index, response := range responses[1:] {
			if response.Room.ID != roomID {
				store.Close()
				t.Fatalf("attempt %d split same-mode joins between %q and join %d room %q", attempt, roomID, index+1, response.Room.ID)
			}
		}
		rooms := store.listRooms().Rooms
		store.Close()
		if len(rooms) != 1 || len(rooms[0].Players) != playerCount {
			t.Fatalf("attempt %d expected one full solo room, got %+v", attempt, rooms)
		}
	}
}

func TestMatchmakingJoinResponseMode(t *testing.T) {
	store := NewStore(5)
	handler := debugHandler(t, store)
	joined := joinMatchmakingWithMode(t, handler, simulation.GameModeTeam)

	listRec := request(handler, http.MethodGet, "/rooms")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected room list status 200, got %d", listRec.Code)
	}
	var listed roomListResponse
	decodeResponse(t, listRec, &listed)
	if len(listed.Rooms) != 1 || listed.Rooms[0].GameMode != joined.GameMode {
		t.Fatalf("expected room list mode %q, got %+v", joined.GameMode, listed.Rooms)
	}

	detailRec := request(handler, http.MethodGet, "/rooms/"+joined.Room.ID)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("expected room detail status 200, got %d", detailRec.Code)
	}
	var detail roomResponse
	decodeResponse(t, detailRec, &detail)
	if detail.GameMode != joined.GameMode || detail.GameMode != joined.Room.GameMode {
		t.Fatalf("expected join/list/detail mode %q, got join room %q and detail %q", joined.GameMode, joined.Room.GameMode, detail.GameMode)
	}
}

func TestMatchmakingJoinGameModeRateLimitPrecedesBodyDecode(t *testing.T) {
	store := NewStore(5)
	defer store.Close()
	handler, err := HandlerWithConfig(store, HandlerConfig{
		JoinLimiter: NewIPRateLimiter(10, 1, nil),
	})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	first := requestWithBody(handler, http.MethodPost, "/matchmaking/join", "")
	if first.Code != http.StatusCreated {
		t.Fatalf("expected first join status 201, got %d", first.Code)
	}
	malformed := requestWithBody(handler, http.MethodPost, "/matchmaking/join", `{"gameMode":`)
	if malformed.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limit before malformed body decode, got %d: %s", malformed.Code, malformed.Body.String())
	}
	assertError(t, malformed, "rate_limited")
}

func TestMatchmakingJoinRejectsOversizedBody(t *testing.T) {
	oversizedBody := `{"gameMode":"solo","padding":"` + strings.Repeat("x", 2*1024) + `"}`

	t.Run("accepted request is capped", func(t *testing.T) {
		store := NewStore(5)
		handler := debugHandler(t, store)

		rec := requestWithBody(handler, http.MethodPost, "/matchmaking/join", oversizedBody)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected oversized body status 400, got %d: %s", rec.Code, rec.Body.String())
		}
		assertError(t, rec, "invalid_request")
		if got := len(store.listRooms().Rooms); got != 0 {
			t.Fatalf("expected oversized body not to create room state, got %d rooms", got)
		}
		store.mu.RLock()
		playerIDCount := len(store.playerIDs)
		store.mu.RUnlock()
		if playerIDCount != 0 {
			t.Fatalf("expected oversized body not to create player state, got %d player IDs", playerIDCount)
		}
	})

	t.Run("rate limit is evaluated first", func(t *testing.T) {
		store := NewStore(5)
		defer store.Close()
		handler, err := HandlerWithConfig(store, HandlerConfig{
			JoinLimiter: NewIPRateLimiter(10, 1, nil),
		})
		if err != nil {
			t.Fatalf("create handler: %v", err)
		}

		first := requestWithBody(handler, http.MethodPost, "/matchmaking/join", "")
		if first.Code != http.StatusCreated {
			t.Fatalf("expected first join status 201, got %d", first.Code)
		}
		roomCount := len(store.listRooms().Rooms)
		store.mu.RLock()
		playerIDCount := len(store.playerIDs)
		store.mu.RUnlock()
		rec := requestWithBody(handler, http.MethodPost, "/matchmaking/join", oversizedBody)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected rate limit before oversized body decode, got %d: %s", rec.Code, rec.Body.String())
		}
		assertError(t, rec, "rate_limited")
		if got := len(store.listRooms().Rooms); got != roomCount {
			t.Fatalf("expected rate-limited oversized request not to mutate rooms, got %d before and %d after", roomCount, got)
		}
		store.mu.RLock()
		gotPlayerIDCount := len(store.playerIDs)
		store.mu.RUnlock()
		if gotPlayerIDCount != playerIDCount {
			t.Fatalf("expected rate-limited oversized request not to mutate player IDs, got %d before and %d after", playerIDCount, gotPlayerIDCount)
		}
	})
}

func TestDebugRoomUsesDefaultMode(t *testing.T) {
	store := NewStore(5)
	handler := debugHandler(t, store)

	created := createRoom(t, handler)
	if created.GameMode != simulation.GameModeDuel1v1 {
		t.Fatalf("expected debug room default mode %q, got %q", simulation.GameModeDuel1v1, created.GameMode)
	}
	stored := store.lookupRoom(created.ID)
	if stored == nil || stored.gameConfig.SelectedMode.ID != simulation.GameModeDuel1v1 {
		t.Fatalf("expected debug room to own default selected config, got %+v", stored)
	}
}

func TestHandlerMatchmakingFirstJoinCreatesWaitingRoomAndReturnsConnectionInfo(t *testing.T) {
	handler := debugHandler(t, NewStore(5))

	rec := request(handler, http.MethodPost, "/matchmaking/join")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected matchmaking join status 201, got %d", rec.Code)
	}

	var joined matchmakingJoinResponse
	decodeResponse(t, rec, &joined)
	if joined.Room.ID == "" {
		t.Fatal("expected room ID to be assigned")
	}
	if joined.Room.Status != RoomStatusWaiting {
		t.Fatalf("expected waiting room, got %q", joined.Room.Status)
	}
	if joined.Player.ID == "" || joined.Player.Team != "red" || joined.Player.Slot != 0 {
		t.Fatalf("unexpected player assignment: %+v", joined.Player)
	}
	assertOpaqueID(t, joined.SessionToken, "", 32)
	wantWebSocketPath := "/rooms/" + joined.Room.ID + "/players/" + joined.Player.ID + "?token=" + joined.SessionToken
	if joined.WebSocketPath != wantWebSocketPath {
		t.Fatal("expected websocket path to match the issued room, player, and session")
	}
	if len(joined.Room.Players) != 1 || joined.Room.Players[0].ID != joined.Player.ID {
		t.Fatalf("expected response room to contain joined player, got %+v", joined.Room.Players)
	}
}

func TestHandlerMatchmakingResponseIncludesMapDataForClientRendering(t *testing.T) {
	handler := debugHandler(t, NewStore(5))

	rec := request(handler, http.MethodPost, "/matchmaking/join")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected matchmaking join status 201, got %d", rec.Code)
	}

	var joined struct {
		Room struct {
			Map simulation.MapData `json:"map"`
		} `json:"room"`
	}
	decodeResponse(t, rec, &joined)

	fixture := simulation.StaticMapFixture()
	if joined.Room.Map.Width != fixture.Width || joined.Room.Map.Height != fixture.Height {
		t.Fatalf("expected map size %dx%d, got %dx%d", fixture.Width, fixture.Height, joined.Room.Map.Width, joined.Room.Map.Height)
	}
	if joined.Room.Map.TileSize != fixture.TileSize {
		t.Fatalf("expected map tile size %f, got %f", fixture.TileSize, joined.Room.Map.TileSize)
	}
	if len(joined.Room.Map.Map) != fixture.Height {
		t.Fatalf("expected map rows %d, got %d", fixture.Height, len(joined.Room.Map.Map))
	}
	if joined.Room.Map.Map[0][0] != simulation.TileWall || joined.Room.Map.Map[1][1] != simulation.TileGround {
		t.Fatalf("expected fixture tile values in response, got %+v", joined.Room.Map.Map)
	}
}

func TestHandlerMatchmakingResponseSerializesMapRowsAsNumberArrays(t *testing.T) {
	handler := debugHandler(t, NewStore(5))

	rec := request(handler, http.MethodPost, "/matchmaking/join")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected matchmaking join status 201, got %d", rec.Code)
	}

	var joined struct {
		Room struct {
			Map struct {
				Rows []json.RawMessage `json:"map"`
			} `json:"map"`
		} `json:"room"`
	}
	decodeResponse(t, rec, &joined)
	if len(joined.Room.Map.Rows) == 0 {
		t.Fatal("expected map rows in matchmaking response")
	}

	var firstRow []int
	if err := json.Unmarshal(joined.Room.Map.Rows[0], &firstRow); err != nil {
		t.Fatalf("expected raw map row to be a JSON number array, got %s: %v", joined.Room.Map.Rows[0], err)
	}
	if len(firstRow) == 0 || firstRow[0] != int(simulation.TileWall) {
		t.Fatalf("expected first map tile to be wall value %d, got %+v", simulation.TileWall, firstRow)
	}
}

func TestHandlerUsesConfiguredMapForResponseCapacityAndStart(t *testing.T) {
	gameMap := customRoomMap()
	store := newStore(5, newFakeClock(), StoreConfig{
		Map:        gameMap,
		GameConfig: singleModeGameConfig(simulation.DefaultGameModeConfig()),
	})
	handler := debugHandler(t, store)
	defer store.Close()

	joined := joinMatchmaking(t, handler)
	if joined.Room.Map.Width != gameMap.Width || joined.Room.Map.Height != gameMap.Height {
		t.Fatalf("expected configured map size %dx%d, got %dx%d", gameMap.Width, gameMap.Height, joined.Room.Map.Width, joined.Room.Map.Height)
	}
	if joined.Room.MaxPlayers != gameMap.MaxPlayers {
		t.Fatalf("expected configured max players %d, got %d", gameMap.MaxPlayers, joined.Room.MaxPlayers)
	}

	second := joinMatchmaking(t, handler)
	if second.Room.ID != joined.Room.ID {
		t.Fatalf("expected second join to use configured waiting room %q, got %q", joined.Room.ID, second.Room.ID)
	}
	if second.Room.Status != RoomStatusWaiting {
		t.Fatalf("expected matched room to wait for ready before start, got %q", second.Room.Status)
	}

	third := joinMatchmaking(t, handler)
	if third.Room.ID == joined.Room.ID {
		t.Fatalf("expected third join to create a new room after configured max players, got %q", third.Room.ID)
	}
}

func TestStoreConfigFallsBackToStaticMapWhenMapIsEmpty(t *testing.T) {
	store := newStore(5, newFakeClock(), StoreConfig{})
	handler := debugHandler(t, store)
	defer store.Close()

	joined := joinMatchmaking(t, handler)
	fixture := simulation.StaticMapFixture()
	if joined.Room.Map.Width != fixture.Width || joined.Room.Map.Height != fixture.Height {
		t.Fatalf("expected fallback map size %dx%d, got %dx%d", fixture.Width, fixture.Height, joined.Room.Map.Width, joined.Room.Map.Height)
	}
	if joined.Room.MaxPlayers != fixture.MaxPlayers {
		t.Fatalf("expected fallback max players %d, got %d", fixture.MaxPlayers, joined.Room.MaxPlayers)
	}
}

func TestHandlerMatchmakingSecondJoinUsesSameRoomAndWaitsForReady(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	defer store.Close()
	handler := debugHandler(t, store)

	first := joinMatchmaking(t, handler)
	second := joinMatchmaking(t, handler)

	if second.Room.ID != first.Room.ID {
		t.Fatalf("expected second join to use room %q, got %q", first.Room.ID, second.Room.ID)
	}
	if second.Player.ID == first.Player.ID {
		t.Fatalf("expected distinct player IDs, got %q", second.Player.ID)
	}
	if second.Room.Status != RoomStatusWaiting {
		t.Fatalf("expected room to wait for ready before start, got %q", second.Room.Status)
	}
	if len(second.Room.Players) != 2 || second.Room.LatestSnapshot.PlayerCount != 2 {
		t.Fatalf("expected two players in matched room, got %+v", second.Room)
	}
	gameplayInterval := time.Second / time.Duration(store.gameConfig.TickRate)
	if got := fakeClock.TickerCount(gameplayInterval); got != 0 {
		t.Fatalf("expected matchmaking join not to create a gameplay ticker before ready, got %d", got)
	}
}

func TestHandlerMatchmakingDoesNotLateJoinStartedRooms(t *testing.T) {
	store := NewStore(5)
	defer store.Close()
	handler := debugHandler(t, store)

	first := joinMatchmaking(t, handler)
	second := joinMatchmaking(t, handler)
	third := joinMatchmaking(t, handler)

	if second.Room.ID != first.Room.ID {
		t.Fatalf("expected first pair to share room, got %q and %q", first.Room.ID, second.Room.ID)
	}
	if third.Room.ID == first.Room.ID {
		t.Fatalf("expected third join to avoid started room %q", first.Room.ID)
	}
	if third.Room.Status != RoomStatusWaiting {
		t.Fatalf("expected third join to create waiting room, got %q", third.Room.Status)
	}
	if len(third.Room.Players) != 1 {
		t.Fatalf("expected new waiting room to contain one player, got %+v", third.Room.Players)
	}
}

func TestHandlerMatchmakingUsesDefaultOneVsOneRules(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	defer store.Close()
	handler := debugHandler(t, store)

	first := joinMatchmaking(t, handler)
	if first.Player.Team != "red" || first.Player.Slot != 0 {
		t.Fatalf("expected first player to be red slot 0, got %+v", first.Player)
	}
	if first.Room.MaxPlayers != 6 || first.Room.MaxPlayers != simulation.StaticMapFixture().MaxPlayers {
		t.Fatalf("expected room maxPlayers to stay at map capacity 6, got %d", first.Room.MaxPlayers)
	}
	if first.Room.LatestSnapshot.PlayerCount != 1 {
		t.Fatalf("expected first room snapshot player count 1, got %+v", first.Room.LatestSnapshot)
	}

	second := joinMatchmaking(t, handler)
	if second.Room.ID != first.Room.ID {
		t.Fatalf("expected second player to join room %q, got %q", first.Room.ID, second.Room.ID)
	}
	if second.Player.Team != "blue" || second.Player.Slot != 0 {
		t.Fatalf("expected second player to be blue slot 0, got %+v", second.Player)
	}
	if second.Room.Status != RoomStatusWaiting {
		t.Fatalf("expected matched REST room status to remain waiting, got %q", second.Room.Status)
	}
	if len(second.Room.Players) != 2 || second.Room.LatestSnapshot.PlayerCount != 2 {
		t.Fatalf("expected matched room to contain two players, got %+v", second.Room)
	}
	gameplayInterval := time.Second / time.Duration(store.gameConfig.TickRate)
	if got := fakeClock.TickerCount(gameplayInterval); got != 0 {
		t.Fatalf("expected matchmaking join not to start a gameplay ticker before ready, got %d", got)
	}

	third := joinMatchmaking(t, handler)
	if third.Room.ID == first.Room.ID {
		t.Fatalf("expected third matchmaking join to create a new room after 1v1 match lock, got %q", third.Room.ID)
	}
}

func TestHandlerMatchmakingUsesConfiguredModeRules(t *testing.T) {
	mode := simulation.GameModeConfig{
		ID:              "test_quartet",
		PlayersPerMatch: 4,
		Teams: []simulation.TeamConfig{
			{Name: simulation.TeamRed, Size: 3},
			{Name: simulation.TeamBlue, Size: 1},
		},
		Rules: simulation.GameModeRulesConfig{
			TeamBehavior: simulation.TeamBehaviorTwoTeams,
			FriendlyFire: false,
		},
	}
	gameConfig := singleModeGameConfig(mode)

	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{GameConfig: gameConfig})
	defer store.Close()
	handler := debugHandler(t, store)

	first := joinMatchmaking(t, handler)
	second := joinMatchmaking(t, handler)
	third := joinMatchmaking(t, handler)
	fourth := joinMatchmaking(t, handler)

	if second.Room.ID != first.Room.ID || third.Room.ID != first.Room.ID || fourth.Room.ID != first.Room.ID {
		t.Fatalf("expected first four players to join configured quartet room %q, got %q, %q, and %q", first.Room.ID, second.Room.ID, third.Room.ID, fourth.Room.ID)
	}
	if first.Player.Team != "red" || first.Player.Slot != 0 {
		t.Fatalf("expected first player to be red slot 0, got %+v", first.Player)
	}
	if second.Player.Team != "blue" || second.Player.Slot != 0 {
		t.Fatalf("expected second player to be blue slot 0, got %+v", second.Player)
	}
	if third.Player.Team != "red" || third.Player.Slot != 1 {
		t.Fatalf("expected third player to be red slot 1, got %+v", third.Player)
	}
	if fourth.Player.Team != "red" || fourth.Player.Slot != 2 {
		t.Fatalf("expected fourth player to be red slot 2, got %+v", fourth.Player)
	}
	if len(fourth.Room.Players) != 4 || fourth.Room.LatestSnapshot.PlayerCount != 4 {
		t.Fatalf("expected configured quartet room to contain four players, got %+v", fourth.Room)
	}
	gameplayInterval := time.Second / time.Duration(store.gameConfig.TickRate)
	if got := fakeClock.TickerCount(gameplayInterval); got != 0 {
		t.Fatalf("expected configured matchmaking not to start a gameplay ticker before ready, got %d", got)
	}

	fifth := joinMatchmaking(t, handler)
	if fifth.Room.ID == first.Room.ID {
		t.Fatalf("expected fifth matchmaking join to create a new room after configured quartet lock, got %q", fifth.Room.ID)
	}
}

func TestHandlerIssuesPlayersWithTeamAndSlot(t *testing.T) {
	handler := debugHandler(t, NewStore(5))
	room := createRoom(t, handler)

	tests := []struct {
		name string
		team string
		slot int
	}{
		{name: "first", team: "red", slot: 0},
		{name: "second", team: "blue", slot: 0},
		{name: "third", team: "red", slot: 1},
		{name: "fourth", team: "blue", slot: 1},
	}
	for _, tt := range tests {
		player := createPlayer(t, handler, room.ID)
		if player.ID == "" || player.Team != tt.team || player.Slot != tt.slot {
			t.Fatalf("unexpected %s player: %+v", tt.name, player)
		}
	}
}

func TestHandlerRejectsPlayerJoinWhenRoomFull(t *testing.T) {
	handler := debugHandler(t, NewStore(5))
	room := createRoom(t, handler)

	for i := 0; i < simulation.StaticMapFixture().MaxPlayers; i++ {
		_ = createPlayer(t, handler, room.ID)
	}

	rec := request(handler, http.MethodPost, "/rooms/"+room.ID+"/players")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected room full status 409, got %d", rec.Code)
	}
	assertError(t, rec, "room_full")
}

func TestHandlerStartRequiresAtLeastOnePlayer(t *testing.T) {
	handler := debugHandler(t, NewStore(5))
	room := createRoom(t, handler)

	emptyStart := request(handler, http.MethodPost, "/rooms/"+room.ID+"/start")
	if emptyStart.Code != http.StatusConflict {
		t.Fatalf("expected empty start status 409, got %d", emptyStart.Code)
	}
	assertError(t, emptyStart, "room_has_no_players")

	_ = request(handler, http.MethodPost, "/rooms/"+room.ID+"/players")
	startRec := request(handler, http.MethodPost, "/rooms/"+room.ID+"/start")
	if startRec.Code != http.StatusOK {
		t.Fatalf("expected start status 200, got %d", startRec.Code)
	}
	var started roomResponse
	decodeResponse(t, startRec, &started)
	if started.Status != RoomStatusStarted {
		t.Fatalf("expected started status, got %q", started.Status)
	}
	if started.LatestSnapshot.PlayerCount != 1 {
		t.Fatalf("expected one player in snapshot summary, got %+v", started.LatestSnapshot)
	}
}

func TestHandlerReturnsJSONErrors(t *testing.T) {
	handler := debugHandler(t, NewStore(5))

	rec := request(handler, http.MethodGet, "/rooms/missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected missing room status 404, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}
	assertError(t, rec, "room_not_found")
}

func TestStoreCleansUpWaitingRoomAfterIdleTTL(t *testing.T) {
	fakeClock := newFakeClockAt(time.Date(2026, 5, 30, 7, 0, 0, 0, time.UTC))
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)

	room := createRoom(t, handler)

	fakeClock.Advance(10*time.Minute - time.Nanosecond)
	if rec := request(handler, http.MethodGet, "/rooms/"+room.ID); rec.Code != http.StatusOK {
		t.Fatalf("expected waiting room before TTL to exist, got status %d", rec.Code)
	}

	fakeClock.Advance(time.Nanosecond)
	fakeClock.TickTicker(janitorInterval, 0)
	waitForRoomDeleted(t, store, room.ID)
	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected waiting room after idle TTL to be cleaned up, got status %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func TestStoreCleansUpHardLifetimeExpiredRoom(t *testing.T) {
	fakeClock := newFakeClockAt(time.Date(2026, 5, 30, 7, 0, 0, 0, time.UTC))
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)

	fakeClock.Advance(time.Hour - time.Nanosecond)
	if rec := request(handler, http.MethodGet, "/rooms/"+room.ID); rec.Code != http.StatusOK {
		t.Fatalf("expected room before hard lifetime to exist, got status %d", rec.Code)
	}

	fakeClock.Advance(time.Nanosecond)
	fakeClock.TickTicker(janitorInterval, 0)
	waitForRoomDeleted(t, store, room.ID)
	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected room after hard lifetime to be cleaned up, got status %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func createRoom(t *testing.T, handler http.Handler) roomResponse {
	t.Helper()

	rec := request(handler, http.MethodPost, "/rooms")
	var room roomResponse
	decodeResponse(t, rec, &room)
	return room
}

func joinMatchmaking(t *testing.T, handler http.Handler) matchmakingJoinResponse {
	t.Helper()

	rec := request(handler, http.MethodPost, "/matchmaking/join")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected matchmaking join status 201, got %d", rec.Code)
	}
	var joined matchmakingJoinResponse
	decodeResponse(t, rec, &joined)
	return joined
}

func joinMatchmakingWithMode(t *testing.T, handler http.Handler, gameMode string) matchmakingJoinResponse {
	t.Helper()

	body := `{"gameMode":` + strconv.Quote(gameMode) + `}`
	rec := requestWithBody(handler, http.MethodPost, "/matchmaking/join", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected matchmaking join status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var joined matchmakingJoinResponse
	decodeResponse(t, rec, &joined)
	return joined
}

func customRoomMap() simulation.MapData {
	return simulation.MapData{
		Width:      7,
		Height:     5,
		Index:      9,
		MaxPlayers: 2,
		TileSize:   simulation.TileSize,
		Map: [][]simulation.TileType{
			{simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall},
			{simulation.TileWall, simulation.TileGround, simulation.TileGround, simulation.TileGround, simulation.TileGround, simulation.TileGround, simulation.TileWall},
			{simulation.TileWall, simulation.TileGround, simulation.TileWall, simulation.TileGround, simulation.TileWall, simulation.TileGround, simulation.TileWall},
			{simulation.TileWall, simulation.TileGround, simulation.TileGround, simulation.TileGround, simulation.TileGround, simulation.TileGround, simulation.TileWall},
			{simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall},
		},
	}
}

func singleModeGameConfig(mode simulation.GameModeConfig) simulation.GameConfig {
	config := simulation.StaticGameConfig()
	config.ModeCatalog = simulation.GameModeCatalogConfig{
		Default: mode.ID,
		Catalog: []simulation.GameModeConfig{mode},
	}
	config.SelectedMode = mode
	return config
}

func request(handler http.Handler, method string, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+testDebugAPIToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func requestWithBody(handler http.Handler, method string, path string, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Authorization", "Bearer "+testDebugAPIToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func debugHandler(t *testing.T, store *Store) http.Handler {
	t.Helper()
	t.Cleanup(store.Close)

	handler, err := HandlerWithConfig(store, HandlerConfig{
		EnableDebugAPI: true,
		DebugAPIToken:  testDebugAPIToken,
		JoinLimiter:    NewIPRateLimiter(1, 10_000, nil),
	})
	if err != nil {
		t.Fatalf("create debug handler: %v", err)
	}
	return handler
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}
	if err := json.NewDecoder(rec.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()

	var body errorResponse
	decodeResponse(t, rec, &body)
	if body.Error.Code != code {
		t.Fatalf("expected error code %q, got %+v", code, body)
	}
}

func assertOpaqueID(t *testing.T, value string, prefix string, wantBytes int) {
	t.Helper()

	if !strings.HasPrefix(value, prefix) {
		t.Fatalf("expected opaque value to use the %q prefix", prefix)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		t.Fatalf("decode opaque value: %v", err)
	}
	if len(decoded) != wantBytes {
		t.Fatalf("expected %d decoded bytes, got %d", wantBytes, len(decoded))
	}
}

func assertInternalError(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
	var body errorResponse
	decodeResponse(t, rec, &body)
	if body.Error.Code != "internal_error" || body.Error.Message != "internal server error" {
		t.Fatalf("expected generic internal error, got %+v", body)
	}
}

func assertRoomHasPlayerAndSessionCount(t *testing.T, store *Store, roomID string, want int) {
	t.Helper()

	room := store.lookupRoom(roomID)
	if room == nil {
		t.Fatalf("expected room %q", roomID)
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	if len(room.Players) != want || len(room.sessions) != want {
		t.Fatalf("expected %d players and sessions, got %d and %d", want, len(room.Players), len(room.sessions))
	}
}

func mustMarshalTestJSON(t *testing.T, value any) []byte {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return payload
}

func assertJSONRouteResponse(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("expected status %d, got %d", status, rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}
	if code != "" {
		var body errorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != code {
			t.Fatalf("expected error code %q, got %+v", code, body)
		}
		wantMessage := map[string]string{
			"method_not_allowed":       "method not allowed",
			"not_found":                "route not found",
			"player_already_connected": "player already connected",
			"player_not_found":         "player not found",
			"room_cap_reached":         "active room cap reached",
			"room_full":                "room full",
			"room_has_no_players":      "room has no players",
			"room_not_found":           "room not found",
		}[code]
		if body.Error.Message != wantMessage {
			t.Fatalf("expected error message %q, got %+v", wantMessage, body)
		}
	}
}
