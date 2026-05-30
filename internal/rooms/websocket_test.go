package rooms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

func TestWebSocketConnectsIssuedPlayerAndBroadcastsSnapshotsOnTicks(t *testing.T) {
	fakeClock := newFakeClock()
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

	fakeClock.Tick()
	first := readSnapshotMessage(t, conn)
	if first.Type != "snapshot" {
		t.Fatalf("expected snapshot message, got %q", first.Type)
	}
	if first.Snapshot.Tick != 1 {
		t.Fatalf("expected first snapshot tick 1, got %d", first.Snapshot.Tick)
	}
	if len(first.Snapshot.Players) != 1 {
		t.Fatalf("expected one player in snapshot, got %+v", first.Snapshot.Players)
	}

	fakeClock.Tick()
	second := readSnapshotMessage(t, conn)
	if second.Snapshot.Tick != 2 {
		t.Fatalf("expected second snapshot tick 2, got %d", second.Snapshot.Tick)
	}
	if fakeClock.RequestedDuration() != time.Second/time.Duration(simulation.TickRate) {
		t.Fatalf("expected 30Hz ticker duration, got %s", fakeClock.RequestedDuration())
	}
}

func TestWebSocketRejectsUnknownRoomOrPlayer(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := Handler(store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)

	_, _, err := websocket.Dial(context.Background(), websocketURL(server.URL, "missing", "player-1"), nil)
	if err == nil {
		t.Fatal("expected unknown room dial to fail")
	}

	_, _, err = websocket.Dial(context.Background(), websocketURL(server.URL, room.ID, "missing-player"), nil)
	if err == nil {
		t.Fatal("expected unknown player dial to fail")
	}
}

func TestWebSocketRejectsDuplicateSamePlayerConnection(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := Handler(store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := createPlayer(t, handler, room.ID)

	first := dialRoomPlayer(t, server.URL, room.ID, player.ID)
	defer first.Close(websocket.StatusNormalClosure, "")

	_, _, err := websocket.Dial(context.Background(), websocketURL(server.URL, room.ID, player.ID), nil)
	if err == nil {
		t.Fatal("expected duplicate player dial to fail")
	}
}

func TestWebSocketAllowsWaitingRoomConnectionWithoutBroadcasting(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := Handler(store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := createPlayer(t, handler, room.ID)

	conn := dialRoomPlayer(t, server.URL, room.ID, player.ID)
	defer conn.Close(websocket.StatusNormalClosure, "")

	fakeClock.Tick()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected no gameplay snapshot before room start")
	}
}

func TestWebSocketKeepsSnapshotStreamAfterInvalidInput(t *testing.T) {
	fakeClock := newFakeClock()
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

	writeText(t, conn, "{not-json")
	fakeClock.Tick()
	message := readSnapshotMessage(t, conn)
	if message.Snapshot.Tick != 1 {
		t.Fatalf("expected stream to continue with tick 1, got %d", message.Snapshot.Tick)
	}
}

func TestWebSocketAppliesValidInputOnNextBroadcastTick(t *testing.T) {
	fakeClock := newFakeClock()
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

	writeWSJSON(t, conn, inputMessage{MoveDir: simulation.Vector2{X: 1, Y: 0}})
	waitForPendingInput(t, store, room.ID, player.ID)
	fakeClock.Tick()
	message := readSnapshotMessage(t, conn)
	if message.Snapshot.Players[0].Pos.X == 0 {
		t.Fatalf("expected valid input to move player, got %+v", message.Snapshot.Players[0].Pos)
	}
}

func TestWebSocketUsesClientCompatibleMessageFieldNames(t *testing.T) {
	fakeClock := newFakeClock()
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

	writeText(t, conn, `{"MoveDir":{"x":1,"y":0},"AttackDir":{"x":0,"y":1},"PressedAttack":true}`)
	waitForPendingInput(t, store, room.ID, player.ID)
	fakeClock.Tick()

	payload := readWebSocketPayload(t, conn)
	text := string(payload)
	for _, want := range []string{
		`"Snapshot"`,
		`"Players"`,
		`"Id":"` + player.ID + `"`,
		`"Pos":{"x":`,
		`"MoveDir":{"x":1`,
		`"AttackDir":{"x":0,"y":1}`,
		`"PressedAttack":true`,
		`"IsDead":false`,
		`"OwnerId":"` + player.ID + `"`,
		`"Dir":{"x":0,"y":1}`,
		`"IsDestroyed":false`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected websocket payload to contain %s, got %s", want, text)
		}
	}
	if strings.Contains(text, `"moveDir"`) || strings.Contains(text, `"ownerID"`) || strings.Contains(text, `"X"`) {
		t.Fatalf("expected client-compatible field names, got %s", text)
	}

	var message snapshotMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode snapshot message: %v", err)
	}
	if len(message.Snapshot.Projectiles) != 1 {
		t.Fatalf("expected attack input to create one projectile, got %+v", message.Snapshot.Projectiles)
	}
}

type fakeClock struct {
	ticks     chan time.Time
	duration  time.Duration
	stopCount int
}

func newFakeClock() *fakeClock {
	return &fakeClock{ticks: make(chan time.Time, 8)}
}

func (c *fakeClock) NewTicker(duration time.Duration) ticker {
	c.duration = duration
	return c
}

func (c *fakeClock) C() <-chan time.Time {
	return c.ticks
}

func (c *fakeClock) Stop() {
	c.stopCount++
}

func (c *fakeClock) Tick() {
	c.ticks <- time.Now()
}

func (c *fakeClock) RequestedDuration() time.Duration {
	return c.duration
}

func waitForPendingInput(t *testing.T, store *Store, roomID string, playerID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		room := store.rooms[roomID]
		_, ok := room.pendingInputs[playerID]
		store.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("expected pending input for player %s", playerID)
}

func createPlayer(t *testing.T, handler http.Handler, roomID string) playerResponse {
	t.Helper()

	rec := request(handler, http.MethodPost, "/rooms/"+roomID+"/players")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected create player status 201, got %d", rec.Code)
	}
	var player playerResponse
	decodeResponse(t, rec, &player)
	return player
}

func startRoom(t *testing.T, handler http.Handler, roomID string) {
	t.Helper()

	rec := request(handler, http.MethodPost, "/rooms/"+roomID+"/start")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected start room status 200, got %d", rec.Code)
	}
}

func dialRoomPlayer(t *testing.T, serverURL string, roomID string, playerID string) *websocket.Conn {
	t.Helper()

	conn, _, err := websocket.Dial(context.Background(), websocketURL(serverURL, roomID, playerID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func websocketURL(serverURL string, roomID string, playerID string) string {
	return "ws" + serverURL[len("http"):] + "/rooms/" + roomID + "/players/" + playerID
}

func writeText(t *testing.T, conn *websocket.Conn, text string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(text)); err != nil {
		t.Fatalf("write websocket text: %v", err)
	}
}

func writeWSJSON(t *testing.T, conn *websocket.Conn, message any) {
	t.Helper()

	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal websocket message: %v", err)
	}
	writeText(t, conn, string(payload))
}

func readSnapshotMessage(t *testing.T, conn *websocket.Conn) snapshotMessage {
	t.Helper()

	payload := readWebSocketPayload(t, conn)

	var message snapshotMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode snapshot message: %v", err)
	}
	return message
}

func readWebSocketPayload(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	return payload
}
