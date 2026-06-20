package rooms

import (
	"context"
	"encoding/json"
	"io"
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
	waitForAttachedClient(t, store, room.ID, player.ID)

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

func TestWebSocketWriteTimeoutStaysWithinRealtimeBudget(t *testing.T) {
	tickBudget := time.Second / time.Duration(simulation.TickRate)
	if webSocketWriteTimeout > tickBudget {
		t.Fatalf("expected websocket write timeout %s to stay within tick budget %s", webSocketWriteTimeout, tickBudget)
	}
	if webSocketWriteTimeout > 10*time.Millisecond {
		t.Fatalf("expected websocket write timeout to stay in 10ms latency budget, got %s", webSocketWriteTimeout)
	}
}

func TestWebSocketRejectsUnknownRoomOrPlayer(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := Handler(store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)

	_, resp, err := websocket.Dial(context.Background(), websocketURL(server.URL, "missing", "player-1"), nil)
	if err == nil {
		t.Fatal("expected unknown room dial to fail")
	}
	assertWebSocketErrorResponse(t, resp, http.StatusNotFound, "room_not_found")

	_, resp, err = websocket.Dial(context.Background(), websocketURL(server.URL, room.ID, "missing-player"), nil)
	if err == nil {
		t.Fatal("expected unknown player dial to fail")
	}
	assertWebSocketErrorResponse(t, resp, http.StatusNotFound, "player_not_found")
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
	waitForAttachedClient(t, store, room.ID, player.ID)

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
	waitForAttachedClient(t, store, room.ID, player.ID)

	writeText(t, conn, "{not-json")
	invalidInput := readErrorMessage(t, conn)
	if invalidInput.Error.Code != "invalid_input" {
		t.Fatalf("expected invalid_input error, got %+v", invalidInput.Error)
	}

	fakeClock.Tick()
	message := readSnapshotMessage(t, conn)
	if message.Snapshot.Tick != 1 {
		t.Fatalf("expected stream to continue with tick 1, got %d", message.Snapshot.Tick)
	}
}

func TestWebSocketSendsErrorMessageAfterInvalidInputAndKeepsSnapshotStream(t *testing.T) {
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
	waitForAttachedClient(t, store, room.ID, player.ID)

	writeText(t, conn, "{not-json")
	errorMessage := readErrorMessage(t, conn)
	if errorMessage.Type != "error" {
		t.Fatalf("expected error message type, got %q", errorMessage.Type)
	}
	if errorMessage.Error.Code != "invalid_input" {
		t.Fatalf("expected invalid_input error, got %+v", errorMessage.Error)
	}

	fakeClock.Tick()
	snapshot := readSnapshotMessage(t, conn)
	if snapshot.Snapshot.Tick != 1 {
		t.Fatalf("expected stream to continue with tick 1, got %d", snapshot.Snapshot.Tick)
	}
}

func TestStoreCleansUpStartedRoomAfterAllPlayersDisconnect(t *testing.T) {
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
	waitForAttachedClient(t, store, room.ID, player.ID)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	waitForDetachedClient(t, store, room.ID, player.ID)

	fakeClock.Advance(5*time.Minute - time.Nanosecond)
	if rec := request(handler, http.MethodGet, "/rooms/"+room.ID); rec.Code != http.StatusOK {
		t.Fatalf("expected disconnected started room before TTL to exist, got status %d", rec.Code)
	}

	fakeClock.Advance(time.Nanosecond)
	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected all-disconnected started room after TTL to be cleaned up, got status %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func TestStoreKeepsConnectedRoomPastDisconnectedCleanupTTL(t *testing.T) {
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

	fakeClock.Advance(5 * time.Minute)
	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected connected room to survive cleanup TTL, got status %d", rec.Code)
	}
}

func TestWaitingRoomAcceptsInputButDoesNotBroadcastSnapshot(t *testing.T) {
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
	waitForAttachedClient(t, store, room.ID, player.ID)

	writeText(t, conn, `{"MoveDir":{"x":1,"y":0}}`)
	waitForPendingInput(t, store, room.ID, player.ID)

	fakeClock.Tick()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected no gameplay snapshot before room start")
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
	waitForAttachedClient(t, store, room.ID, player.ID)

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
	waitForAttachedClient(t, store, room.ID, player.ID)

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
		`"HP":100`,
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

func TestWebSocketBroadcastsTwoPlayerMovementHitHPAndDeathSnapshots(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := Handler(store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	red := createPlayer(t, handler, room.ID)
	blue := createPlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	redConn := dialRoomPlayer(t, server.URL, room.ID, red.ID)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialRoomPlayer(t, server.URL, room.ID, blue.ID)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, red.ID)
	waitForAttachedClient(t, store, room.ID, blue.ID)

	var movement snapshotMessage
	for i := 0; i < 36; i++ {
		writeWSJSON(t, redConn, inputMessage{MoveDir: simulation.Vector2{X: 1, Y: 0}})
		waitForPendingInput(t, store, room.ID, red.ID)
		movement = tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
	}
	redPlayer := findSnapshotPlayer(t, movement.Snapshot, simulation.PlayerID(red.ID))
	bluePlayer := findSnapshotPlayer(t, movement.Snapshot, simulation.PlayerID(blue.ID))
	if redPlayer.Pos.X <= 0 {
		t.Fatalf("expected red movement to be visible in both snapshots, got %+v", redPlayer.Pos)
	}
	if bluePlayer.HP != simulation.DefaultPlayerHP || bluePlayer.IsDead {
		t.Fatalf("expected blue to start alive at full HP, got %+v", bluePlayer)
	}

	expectedHP := simulation.DefaultPlayerHP
	var hit snapshotMessage
	for hitCount := 0; hitCount < 10; hitCount++ {
		writeWSJSON(t, redConn, inputMessage{
			AttackDir:     simulation.Vector2{X: 0, Y: -1},
			PressedAttack: true,
		})
		waitForPendingInput(t, store, room.ID, red.ID)
		hit = tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)

		for i := 0; i < 8; i++ {
			hit = tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
			bluePlayer = findSnapshotPlayer(t, hit.Snapshot, simulation.PlayerID(blue.ID))
			if bluePlayer.HP < expectedHP {
				expectedHP -= simulation.DefaultProjectileDamage
				if bluePlayer.HP != expectedHP {
					t.Fatalf("expected blue HP %f after hit %d, got %+v", expectedHP, hitCount+1, bluePlayer)
				}
				break
			}
		}
		if bluePlayer.HP != expectedHP {
			t.Fatalf("expected hit %d to be observed by both clients, last blue state %+v", hitCount+1, bluePlayer)
		}
	}

	bluePlayer = findSnapshotPlayer(t, hit.Snapshot, simulation.PlayerID(blue.ID))
	if bluePlayer.HP != 0 || !bluePlayer.IsDead {
		t.Fatalf("expected blue death state in both snapshots, got %+v", bluePlayer)
	}
	if len(hit.Snapshot.Projectiles) == 0 {
		t.Fatal("expected hit/death snapshot to include projectile history")
	}
}

type fakeClock struct {
	ticks     chan time.Time
	duration  time.Duration
	stopCount int
	now       time.Time
}

func newFakeClock() *fakeClock {
	return newFakeClockAt(time.Date(2026, 5, 30, 7, 0, 0, 0, time.UTC))
}

func newFakeClockAt(now time.Time) *fakeClock {
	return &fakeClock{ticks: make(chan time.Time, 8), now: now}
}

func (c *fakeClock) Now() time.Time {
	return c.now
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

func (c *fakeClock) Advance(duration time.Duration) {
	c.now = c.now.Add(duration)
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

func waitForAttachedClient(t *testing.T, store *Store, roomID string, playerID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		room := store.rooms[roomID]
		conn := room.clients[playerID]
		store.mu.Unlock()
		if conn != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("expected attached websocket client for player %s", playerID)
}

func waitForDetachedClient(t *testing.T, store *Store, roomID string, playerID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		room := store.rooms[roomID]
		_, ok := room.clients[playerID]
		store.mu.Unlock()
		if !ok {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("expected detached websocket client for player %s", playerID)
}

func tickAndReadMatchingSnapshots(t *testing.T, fakeClock *fakeClock, first *websocket.Conn, second *websocket.Conn) snapshotMessage {
	t.Helper()

	fakeClock.Tick()
	firstMessage := readSnapshotMessage(t, first)
	secondMessage := readSnapshotMessage(t, second)
	assertMatchingSnapshots(t, firstMessage, secondMessage)
	return firstMessage
}

func assertMatchingSnapshots(t *testing.T, first snapshotMessage, second snapshotMessage) {
	t.Helper()

	firstPayload, err := json.Marshal(first.Snapshot)
	if err != nil {
		t.Fatalf("marshal first snapshot: %v", err)
	}
	secondPayload, err := json.Marshal(second.Snapshot)
	if err != nil {
		t.Fatalf("marshal second snapshot: %v", err)
	}
	if string(firstPayload) != string(secondPayload) {
		t.Fatalf("expected matching snapshots, got first %s and second %s", firstPayload, secondPayload)
	}
}

func findSnapshotPlayer(t *testing.T, snapshot simulation.Snapshot, playerID simulation.PlayerID) simulation.PlayerData {
	t.Helper()

	for _, player := range snapshot.Players {
		if player.ID == playerID {
			return player
		}
	}
	t.Fatalf("expected snapshot to include player %s", playerID)
	return simulation.PlayerData{}
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

func readErrorMessage(t *testing.T, conn *websocket.Conn) errorMessage {
	t.Helper()

	payload := readWebSocketPayload(t, conn)

	var message errorMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode error message: %v", err)
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

func assertWebSocketErrorResponse(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()

	if resp == nil {
		t.Fatalf("expected websocket error response with status %d", status)
	}
	defer resp.Body.Close()

	if resp.StatusCode != status {
		t.Fatalf("expected websocket response status %d, got %d", status, resp.StatusCode)
	}
	var body errorResponse
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read websocket error response: %v", err)
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode websocket error response: %v", err)
	}
	if body.Error.Code != code {
		t.Fatalf("expected websocket error code %q, got %+v", code, body.Error)
	}
}
