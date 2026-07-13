package rooms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

func TestRoomLifecycleLogsOnlyCommittedTransitions(t *testing.T) {
	logs := &lockedLogBuffer{}
	store := NewStoreWithConfig(1, StoreConfig{Logger: jsonTestLogger(logs)})
	t.Cleanup(store.Close)

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.createRoom(); err == nil {
		t.Fatal("expected capped create to fail")
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("start room: %v", err)
	}
	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("duplicate start: %v", err)
	}

	assertLogEventCount(t, logs, "room_created", 1)
	assertLogEventCount(t, logs, "room_started", 1)
}

func TestRoomCreatedWaitsForRegistryInsertionAndCredentialSuccess(t *testing.T) {
	t.Run("credential failure", func(t *testing.T) {
		logs := &lockedLogBuffer{}
		random := bytes.NewReader(bytes.Join([][]byte{
			bytes.Repeat([]byte{0x11}, 16),
			bytes.Repeat([]byte{0x12}, 16),
			bytes.Repeat([]byte{0x13}, 31),
		}, nil))
		store := NewStoreWithConfig(5, StoreConfig{Random: random, Logger: jsonTestLogger(logs)})
		t.Cleanup(store.Close)

		if _, err := store.joinMatchmaking(); err == nil {
			t.Fatal("expected matchmaking credential failure")
		}
		assertLogEventCount(t, logs, "room_created", 0)
	})

	t.Run("committed matchmaking room", func(t *testing.T) {
		var store *Store
		var callbackErr string
		callbackCount := 0
		handler := &callbackLogHandler{handle: func(record slog.Record) {
			if record.Message != "room_created" {
				return
			}
			callbackCount++
			if !store.mu.TryRLock() {
				callbackErr = "logger called while Store.mu was held"
				return
			}
			defer store.mu.RUnlock()
			roomID := logRecordString(record, "roomID")
			if store.rooms[roomID] == nil {
				callbackErr = "logger called before registry insertion"
			}
		}}
		store = NewStoreWithConfig(5, StoreConfig{Logger: slog.New(handler)})
		t.Cleanup(store.Close)

		joined, err := store.joinMatchmaking()
		if err != nil {
			t.Fatalf("join matchmaking: %v", err)
		}
		if callbackErr != "" {
			t.Fatal(callbackErr)
		}
		if callbackCount != 1 {
			t.Fatalf("expected one committed room_created log, got %d", callbackCount)
		}
		if store.lookupRoom(joined.Room.ID) == nil {
			t.Fatal("expected committed matchmaking room in registry")
		}
	})
}

func TestRoomStartedLogsOnceAcrossManualCountdownRace(t *testing.T) {
	logs := &lockedLogBuffer{}
	store := newStore(5, newFakeClock(), StoreConfig{Logger: jsonTestLogger(logs)})
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add player: %v", err)
	}

	countdownTicker := newCountingTicker()
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.matchStatus = MatchStatusStarting
	room.countdown = 1
	room.countdownTicker = countdownTicker
	room.countdownStop = make(chan struct{})
	room.mu.Unlock()

	if completed := store.tickMatchCountdownRoom(room, countdownTicker); !completed {
		t.Fatal("expected countdown to complete")
	}
	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("manual start after countdown: %v", err)
	}

	assertLogEventCount(t, logs, "room_started", 1)

	concurrentLogs := &lockedLogBuffer{}
	concurrentStore := newStore(5, newFakeClock(), StoreConfig{Logger: jsonTestLogger(concurrentLogs)})
	t.Cleanup(concurrentStore.Close)
	concurrentCreated, err := concurrentStore.createRoom()
	if err != nil {
		t.Fatalf("create concurrent room: %v", err)
	}
	if _, err := concurrentStore.addPlayer(concurrentCreated.ID); err != nil {
		t.Fatalf("add concurrent player: %v", err)
	}
	concurrentTicker := newCountingTicker()
	concurrentRoom := concurrentStore.lookupRoom(concurrentCreated.ID)
	concurrentRoom.mu.Lock()
	concurrentRoom.matchStatus = MatchStatusStarting
	concurrentRoom.countdown = 1
	concurrentRoom.countdownTicker = concurrentTicker
	concurrentRoom.countdownStop = make(chan struct{})
	concurrentRoom.mu.Unlock()

	start := make(chan struct{})
	manualResult := make(chan error, 1)
	countdownResult := make(chan bool, 1)
	var racers sync.WaitGroup
	racers.Add(2)
	go func() {
		defer racers.Done()
		<-start
		_, startErr := concurrentStore.startRoom(concurrentCreated.ID)
		manualResult <- startErr
	}()
	go func() {
		defer racers.Done()
		<-start
		countdownResult <- concurrentStore.tickMatchCountdownRoom(concurrentRoom, concurrentTicker)
	}()
	close(start)
	racers.Wait()
	if err := <-manualResult; err != nil {
		t.Fatalf("concurrent manual start: %v", err)
	}
	if completed := <-countdownResult; !completed {
		t.Fatal("expected concurrent countdown path to finish")
	}
	assertLogEventCount(t, concurrentLogs, "room_started", 1)
}

func TestRoomEndedAndExpiredLogOnlySuccessfulDelete(t *testing.T) {
	t.Run("game end", func(t *testing.T) {
		logs := &lockedLogBuffer{}
		store := newStore(5, newFakeClock(), StoreConfig{Logger: jsonTestLogger(logs)})
		t.Cleanup(store.Close)
		started := createStartedRoomInStore(t, store)
		room := store.lookupRoom(started.ID)
		room.mu.Lock()
		room.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
			return simulation.Snapshot{Players: []simulation.PlayerData{{
				ID:     simulation.PlayerID(started.Players[0].ID),
				IsDead: true,
			}}}
		})
		room.mu.Unlock()

		store.tickRoomState(room)
		store.tickRoomState(room)

		assertLogEventCount(t, logs, "room_ended", 1)
	})

	t.Run("stale game end", func(t *testing.T) {
		logs := &lockedLogBuffer{}
		store := newStore(5, newFakeClock(), StoreConfig{Logger: jsonTestLogger(logs)})
		t.Cleanup(store.Close)
		started := createStartedRoomInStore(t, store)
		original := store.lookupRoom(started.ID)
		original.mu.Lock()
		original.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
			return simulation.Snapshot{Players: []simulation.PlayerData{{
				ID:     simulation.PlayerID(started.Players[0].ID),
				IsDead: true,
			}}}
		})
		original.mu.Unlock()
		store.mu.Lock()
		replacement := store.newRoomLocked(started.ID)
		store.rooms[started.ID] = replacement
		store.mu.Unlock()

		store.tickRoomState(original)

		assertLogEventCount(t, logs, "room_ended", 0)
	})

	t.Run("expiry", func(t *testing.T) {
		logs := &lockedLogBuffer{}
		clock := newFakeClock()
		store := newStore(5, clock, StoreConfig{Logger: jsonTestLogger(logs)})
		t.Cleanup(store.Close)
		if _, err := store.createRoom(); err != nil {
			t.Fatalf("create room: %v", err)
		}
		clock.Advance(defaultWaitingRoomIdleTTL)

		if deleted := store.cleanupExpired(clock.Now()); deleted != 1 {
			t.Fatalf("expected one expired room, got %d", deleted)
		}
		if deleted := store.cleanupExpired(clock.Now().Add(time.Second)); deleted != 0 {
			t.Fatalf("expected duplicate cleanup to delete nothing, got %d", deleted)
		}

		assertLogEventCount(t, logs, "room_expired", 1)
	})
}

func TestWebSocketLifecycleLogsOnceAcrossReadWritePingAndCloseRace(t *testing.T) {
	t.Run("connected publication precedes immediate disconnect", func(t *testing.T) {
		handler := newOrderedLifecycleLogHandler()
		store := newStore(5, newFakeClock(), StoreConfig{Logger: slog.New(handler)})
		t.Cleanup(store.Close)
		created, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		issued, err := store.addPlayer(created.ID)
		if err != nil {
			t.Fatalf("add player: %v", err)
		}
		if _, err := store.startRoom(created.ID); err != nil {
			t.Fatalf("start room: %v", err)
		}
		reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
		if err != nil {
			t.Fatalf("reserve client: %v", err)
		}

		attachDone := make(chan struct{})
		var session *clientSession
		var attached bool
		go func() {
			session, attached = store.attachClientSession(reservation, newFakeClientConn(false))
			close(attachDone)
		}()
		select {
		case <-handler.connectedStarted:
		case <-time.After(time.Second):
			close(handler.allowConnected)
			<-attachDone
			t.Fatal("expected websocket_connected publication")
		}

		room := store.lookupRoom(created.ID)
		room.mu.Lock()
		session = room.clients[issued.Player.ID]
		room.mu.Unlock()
		if session == nil {
			close(handler.allowConnected)
			<-attachDone
			t.Fatal("expected attached session before connected publication completed")
		}
		closeDone := make(chan struct{})
		go func() {
			session.close(websocket.StatusNormalClosure, "test close")
			close(closeDone)
		}()
		waitForDetachedClient(t, store, created.ID, issued.Player.ID)
		select {
		case <-handler.disconnectedPublished:
			close(handler.allowConnected)
			<-attachDone
			<-closeDone
			t.Fatal("websocket_disconnected published before websocket_connected completed")
		default:
		}
		close(handler.allowConnected)
		<-attachDone
		<-closeDone
		if !attached {
			t.Fatal("expected client attach")
		}
		if got := handler.eventsSnapshot(); !slices.Equal(got, []string{"websocket_connected", "websocket_disconnected"}) {
			t.Fatalf("expected ordered lifecycle logs, got %v", got)
		}
	})

	t.Run("write ping and close race", func(t *testing.T) {
		logs := &lockedLogBuffer{}
		clock := newFakeClock()
		store := newStore(5, clock, StoreConfig{
			Logger:            jsonTestLogger(logs),
			HeartbeatInterval: time.Second,
		})
		t.Cleanup(store.Close)
		created, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		issued, err := store.addPlayer(created.ID)
		if err != nil {
			t.Fatalf("add player: %v", err)
		}
		if _, err := store.startRoom(created.ID); err != nil {
			t.Fatalf("start room: %v", err)
		}
		reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
		if err != nil {
			t.Fatalf("reserve client: %v", err)
		}

		failureGate := make(chan struct{})
		pingStarted := make(chan struct{})
		conn := newFakeClientConn(false)
		conn.writeFn = func(context.Context, []byte) error {
			<-failureGate
			return errors.New("private write failure sentinel")
		}
		conn.pingFn = func(context.Context) error {
			close(pingStarted)
			<-failureGate
			return errors.New("private ping failure sentinel")
		}
		session, attached := store.attachClientSession(reservation, conn)
		if !attached {
			t.Fatal("expected client attach")
		}
		if !session.enqueueControl([]byte("control")) {
			t.Fatal("expected control enqueue")
		}
		select {
		case <-conn.writeStarted:
		case <-time.After(time.Second):
			t.Fatal("expected writer to start")
		}
		clock.TickTicker(time.Second, 0)
		select {
		case <-pingStarted:
		case <-time.After(time.Second):
			t.Fatal("expected heartbeat to start")
		}

		close(failureGate)
		<-session.closeDone
		<-session.writerDone
		<-session.heartbeatDone

		assertLogEventCount(t, logs, "websocket_connected", 1)
		assertLogEventCount(t, logs, "websocket_disconnected", 1)
		assertLogEventCount(t, logs, "websocket_io_error", 1)
		for _, forbidden := range []string{"private write failure sentinel", "private ping failure sentinel"} {
			if strings.Contains(logs.String(), forbidden) {
				t.Fatalf("websocket lifecycle log leaked raw transport error %q: %s", forbidden, logs.String())
			}
		}
		assertStructuredLogSchema(t, logs)
	})
}

func TestWebSocketReconnectIgnoresStaleSessionObservation(t *testing.T) {
	logs := &lockedLogBuffer{}
	observer := &recordingObserver{}
	store := newStore(5, newFakeClock(), StoreConfig{Logger: jsonTestLogger(logs), Observer: observer})
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("start room: %v", err)
	}

	oldReservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve old client: %v", err)
	}
	allowOldClose := make(chan struct{})
	oldConn := newFakeClientConn(false)
	oldConn.closeBlock = allowOldClose
	oldConn.closeStarted = make(chan struct{})
	oldSession, attached := store.attachClientSession(oldReservation, oldConn)
	if !attached {
		t.Fatal("expected old client attach")
	}
	oldCloseDone := make(chan struct{})
	go func() {
		oldSession.close(websocket.StatusNormalClosure, "old close")
		close(oldCloseDone)
	}()
	select {
	case <-oldConn.closeStarted:
	case <-time.After(time.Second):
		close(allowOldClose)
		t.Fatal("expected old close to reach transport")
	}

	reconnectReservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
	if err != nil {
		close(allowOldClose)
		t.Fatalf("reserve reconnect: %v", err)
	}
	currentSession, attached := store.attachClientSession(reconnectReservation, newFakeClientConn(false))
	if !attached {
		close(allowOldClose)
		t.Fatal("expected reconnect attach")
	}
	store.releaseClient(oldReservation, oldSession)
	close(allowOldClose)
	<-oldCloseDone

	assertLogEventCount(t, logs, "websocket_connected", 2)
	assertLogEventCount(t, logs, "websocket_disconnected", 1)
	assertObserverValues(t, observer.connectedClientValues(), []int{1, 0, 1})

	currentSession.close(websocket.StatusNormalClosure, "current close")
	assertLogEventCount(t, logs, "websocket_disconnected", 2)
	assertObserverValues(t, observer.connectedClientValues(), []int{1, 0, 1, 0})
}

func TestStructuredLogsRedactSecretsAndPeerControlledText(t *testing.T) {
	logs := &lockedLogBuffer{}
	store := newStore(5, newFakeClock(), StoreConfig{Logger: jsonTestLogger(logs)})
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("start room: %v", err)
	}

	const (
		secretToken = "private-session-token-sentinel"
		peerReason  = "private-peer-close-reason-sentinel"
		rawError    = "private-raw-error-sentinel"
	)
	pathWithoutToken := strings.SplitN(issued.WebSocketPath, "?", 2)[0]
	recorder := request(Handler(store), http.MethodGet, pathWithoutToken+"?token="+secretToken+"&private=query-sentinel")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized status, got %d", recorder.Code)
	}

	reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
	if !attached {
		t.Fatal("expected client attach")
	}
	recordWebSocketReadError(session, websocket.CloseError{Code: websocket.StatusPolicyViolation, Reason: peerReason})
	session.close(websocket.StatusNormalClosure, "")

	// The structured logging boundary must reject fields that could carry raw
	// credentials, query strings, peer reasons, or transport errors even if a
	// future caller accidentally supplies them.
	store.logWebSocketEvent("websocket_io_error", created.ID, issued.Player.ID,
		"category", "read_failed",
		"status", "policy_violation",
		"token", secretToken,
		"query", "private=query-sentinel",
		"reason", peerReason,
		"error", errors.New(rawError),
	)

	assertLogEventCount(t, logs, "websocket_auth_rejected", 1)
	assertLogEventCount(t, logs, "websocket_connected", 1)
	assertLogEventCount(t, logs, "websocket_disconnected", 1)
	assertLogEventCount(t, logs, "websocket_io_error", 2)
	assertLogEventFields(t, logs, "websocket_auth_rejected", map[string]string{"category": "invalid_token"})
	assertLogEventFields(t, logs, "websocket_io_error", map[string]string{"category": "read_close", "status": "policy_violation"})
	for _, forbidden := range []string{secretToken, "private=query-sentinel", peerReason, rawError} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("structured logs leaked forbidden text %q: %s", forbidden, logs.String())
		}
	}
	assertStructuredLogSchema(t, logs)
}

func assertLogEventFields(t *testing.T, logs *lockedLogBuffer, event string, fields map[string]string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if record["event"] != event {
			continue
		}
		matches := true
		for key, want := range fields {
			if record[key] != want {
				matches = false
				break
			}
		}
		if matches {
			return
		}
	}
	t.Fatalf("expected %s log with fields %v in %s", event, fields, logs.String())
}

type lockedLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedLogBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(payload)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func jsonTestLogger(buffer *lockedLogBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buffer, nil))
}

type callbackLogHandler struct {
	handle func(slog.Record)
}

func (h *callbackLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *callbackLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.handle(record)
	return nil
}

func (h *callbackLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *callbackLogHandler) WithGroup(string) slog.Handler { return h }

func logRecordString(record slog.Record, key string) string {
	var value string
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			value = attr.Value.String()
			return false
		}
		return true
	})
	return value
}

type orderedLifecycleLogHandler struct {
	mu                    sync.Mutex
	events                []string
	connectedStarted      chan struct{}
	allowConnected        chan struct{}
	disconnectedPublished chan struct{}
	connectedOnce         sync.Once
	disconnectedOnce      sync.Once
}

func newOrderedLifecycleLogHandler() *orderedLifecycleLogHandler {
	return &orderedLifecycleLogHandler{
		connectedStarted:      make(chan struct{}),
		allowConnected:        make(chan struct{}),
		disconnectedPublished: make(chan struct{}),
	}
}

func (*orderedLifecycleLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *orderedLifecycleLogHandler) Handle(_ context.Context, record slog.Record) error {
	event := logRecordString(record, "event")
	if event == "websocket_connected" {
		h.connectedOnce.Do(func() { close(h.connectedStarted) })
		<-h.allowConnected
	}
	if event != "websocket_connected" && event != "websocket_disconnected" {
		return nil
	}
	h.mu.Lock()
	h.events = append(h.events, event)
	h.mu.Unlock()
	if event == "websocket_disconnected" {
		h.disconnectedOnce.Do(func() { close(h.disconnectedPublished) })
	}
	return nil
}

func (h *orderedLifecycleLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *orderedLifecycleLogHandler) WithGroup(string) slog.Handler { return h }

func (h *orderedLifecycleLogHandler) eventsSnapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.events)
}

func assertLogEventCount(t *testing.T, logs *lockedLogBuffer, event string, want int) {
	t.Helper()
	got := 0
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if record["event"] == event {
			got++
		}
	}
	if got != want {
		t.Fatalf("expected %s count %d, got %d in %s", event, want, got, logs.String())
	}
}

func assertStructuredLogSchema(t *testing.T, logs *lockedLogBuffer) {
	t.Helper()
	allowedEvents := map[string]bool{
		"room_created": true, "room_started": true, "room_ended": true, "room_expired": true,
		"websocket_connected": true, "websocket_disconnected": true,
		"websocket_auth_rejected": true, "websocket_io_error": true,
	}
	allowedCategories := map[string]bool{
		"invalid_token": true, "read_failed": true, "write_failed": true,
		"ping_failed": true, "ping_timeout": true, "read_close": true,
	}
	allowedStatuses := map[string]bool{
		"policy_violation": true, "unsupported_data": true, "invalid_payload": true,
		"message_too_big": true, "internal_error": true, "abnormal_closure": true, "other": true,
	}
	allowedKeys := map[string]bool{
		"time": true, "level": true, "msg": true, "event": true,
		"roomID": true, "playerID": true, "category": true, "status": true,
	}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		event, _ := record["event"].(string)
		if !allowedEvents[event] {
			t.Fatalf("unexpected event %q in %s", event, line)
		}
		if category, ok := record["category"].(string); ok && !allowedCategories[category] {
			t.Fatalf("unbounded category %q in %s", category, line)
		}
		if status, ok := record["status"].(string); ok && !allowedStatuses[status] {
			t.Fatalf("unbounded status %q in %s", status, line)
		}
		for key := range record {
			if !allowedKeys[key] {
				t.Fatalf("unexpected structured log field %q in %s", key, line)
			}
		}
	}
}
