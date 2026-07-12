package rooms

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

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
		_, err = store.joinMatchmaking()
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
		if err := store.reserveClient("missing", "player-1"); !errors.Is(err, ErrRoomNotFound) {
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
		if err := store.reserveClient(room.ID, "missing"); !errors.Is(err, ErrPlayerNotFound) {
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
		if err := store.reserveClient(room.ID, player.ID); err != nil {
			t.Fatalf("reserve first client: %v", err)
		}
		if err := store.reserveClient(room.ID, player.ID); !errors.Is(err, ErrPlayerAlreadyConnected) {
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
			handler := Handler(store)
			room := createRoom(t, handler)
			player := createPlayer(t, handler, room.ID)

			rec := request(handler, http.MethodGet, tt.path(room, player))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
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
			handler := Handler(store)
			room := createRoom(t, handler)
			player := createPlayer(t, handler, room.ID)

			rec := request(handler, tt.method, tt.path(room, player))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
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
			handler := Handler(store)
			room := createRoom(t, handler)

			rec := request(handler, tt.method, tt.path(room))
			assertJSONRouteResponse(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestHandlerHeadContract(t *testing.T) {
	tests := []struct {
		name string
		path func(roomResponse, playerResponse) string
	}{
		{name: "room collection", path: func(roomResponse, playerResponse) string { return "/rooms" }},
		{name: "room detail", path: func(room roomResponse, _ playerResponse) string { return "/rooms/" + room.ID }},
		{name: "websocket path", path: func(room roomResponse, player playerResponse) string {
			return "/rooms/" + room.ID + "/players/" + player.ID
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			handler := Handler(store)
			room := createRoom(t, handler)
			player := createPlayer(t, handler, room.ID)

			rec := request(handler, http.MethodHead, tt.path(room, player))
			assertJSONRouteResponse(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
		})
	}
}

func TestHandlerListsAndCreatesRooms(t *testing.T) {
	handler := Handler(NewStore(5))

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
	handler := Handler(NewStore(5))

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
	handler := Handler(store)
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
	handler := Handler(NewStore(5))

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
	handler := Handler(NewStore(5))

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

func TestHandlerDeletesSingleRoomAndStopsResources(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := Handler(store)
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
	if fakeClock.stopCount != 1 {
		t.Fatalf("expected room ticker to stop once, got %d", fakeClock.stopCount)
	}

	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected deleted room status 404, got %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func TestHandlerMatchmakingFirstJoinCreatesWaitingRoomAndReturnsConnectionInfo(t *testing.T) {
	handler := Handler(NewStore(5))

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
	wantWebSocketPath := "/rooms/" + joined.Room.ID + "/players/" + joined.Player.ID
	if joined.WebSocketPath != wantWebSocketPath {
		t.Fatalf("expected websocket path %q, got %q", wantWebSocketPath, joined.WebSocketPath)
	}
	if len(joined.Room.Players) != 1 || joined.Room.Players[0].ID != joined.Player.ID {
		t.Fatalf("expected response room to contain joined player, got %+v", joined.Room.Players)
	}
}

func TestHandlerMatchmakingResponseIncludesMapDataForClientRendering(t *testing.T) {
	handler := Handler(NewStore(5))

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
	handler := Handler(NewStore(5))

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
	store := newStore(5, newFakeClock(), StoreConfig{Map: gameMap})
	handler := Handler(store)
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
	handler := Handler(store)
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
	handler := Handler(store)

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
	if fakeClock.RequestedDuration() != 0 {
		t.Fatalf("expected matchmaking join not to create ticker before ready, got %s", fakeClock.RequestedDuration())
	}
}

func TestHandlerMatchmakingDoesNotLateJoinStartedRooms(t *testing.T) {
	store := NewStore(5)
	defer store.Close()
	handler := Handler(store)

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
	handler := Handler(store)

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
	if fakeClock.RequestedDuration() != 0 {
		t.Fatalf("expected matchmaking join not to start ticker before ready, got %s", fakeClock.RequestedDuration())
	}

	third := joinMatchmaking(t, handler)
	if third.Room.ID == first.Room.ID {
		t.Fatalf("expected third matchmaking join to create a new room after 1v1 match lock, got %q", third.Room.ID)
	}
}

func TestHandlerMatchmakingUsesConfiguredModeRules(t *testing.T) {
	gameConfig := simulation.StaticGameConfig()
	gameConfig.Mode = simulation.GameModeConfig{
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

	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{GameConfig: gameConfig})
	defer store.Close()
	handler := Handler(store)

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
	if fakeClock.RequestedDuration() != 0 {
		t.Fatalf("expected configured matchmaking not to start ticker before ready, got %s", fakeClock.RequestedDuration())
	}

	fifth := joinMatchmaking(t, handler)
	if fifth.Room.ID == first.Room.ID {
		t.Fatalf("expected fifth matchmaking join to create a new room after configured quartet lock, got %q", fifth.Room.ID)
	}
}

func TestHandlerIssuesPlayersWithTeamAndSlot(t *testing.T) {
	handler := Handler(NewStore(5))
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
	handler := Handler(NewStore(5))
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
	handler := Handler(NewStore(5))
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
	handler := Handler(NewStore(5))

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
	handler := Handler(store)

	room := createRoom(t, handler)

	fakeClock.Advance(10*time.Minute - time.Nanosecond)
	if rec := request(handler, http.MethodGet, "/rooms/"+room.ID); rec.Code != http.StatusOK {
		t.Fatalf("expected waiting room before TTL to exist, got status %d", rec.Code)
	}

	fakeClock.Advance(time.Nanosecond)
	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected waiting room after idle TTL to be cleaned up, got status %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func TestStoreCleansUpHardLifetimeExpiredRoom(t *testing.T) {
	fakeClock := newFakeClockAt(time.Date(2026, 5, 30, 7, 0, 0, 0, time.UTC))
	store := NewStoreWithClock(5, fakeClock)
	handler := Handler(store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := createPlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	conn := dialRoomPlayer(t, server.URL, room.ID, player.ID)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)

	fakeClock.Advance(time.Hour - time.Nanosecond)
	if rec := request(handler, http.MethodGet, "/rooms/"+room.ID); rec.Code != http.StatusOK {
		t.Fatalf("expected room before hard lifetime to exist, got status %d", rec.Code)
	}

	fakeClock.Advance(time.Nanosecond)
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

func request(handler http.Handler, method string, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
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

func assertJSONRouteResponse(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("expected status %d, got %d with body %s", status, rec.Code, rec.Body.String())
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
			"method_not_allowed": "method not allowed",
			"not_found":          "route not found",
			"player_not_found":   "player not found",
			"room_not_found":     "room not found",
		}[code]
		if body.Error.Message != wantMessage {
			t.Fatalf("expected error message %q, got %+v", wantMessage, body)
		}
	}
}
