package rooms

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

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

func TestHandlerIssuesPlayersWithTeamAndSlot(t *testing.T) {
	handler := Handler(NewStore(5))
	room := createRoom(t, handler)

	firstRec := request(handler, http.MethodPost, "/rooms/"+room.ID+"/players")
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("expected first player status 201, got %d", firstRec.Code)
	}
	var first playerResponse
	decodeResponse(t, firstRec, &first)
	if first.ID == "" || first.Team != "red" || first.Slot != 0 {
		t.Fatalf("unexpected first player: %+v", first)
	}

	secondRec := request(handler, http.MethodPost, "/rooms/"+room.ID+"/players")
	var second playerResponse
	decodeResponse(t, secondRec, &second)
	if second.ID == "" || second.Team != "blue" || second.Slot != 0 {
		t.Fatalf("unexpected second player: %+v", second)
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
