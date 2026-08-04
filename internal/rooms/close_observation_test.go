package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/observability"
	"nhooyr.io/websocket"
)

func TestWebSocketCloseObservationRecordsBoundedCauseAndContextOnce(t *testing.T) {
	logs := &lockedLogBuffer{}
	clock := newFakeClock()
	metrics := observability.NewMetrics()
	store := newStore(5, clock, StoreConfig{
		Logger:   jsonTestLogger(logs),
		Observer: metrics,
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
	conn := newFakeClientConn(false)
	session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)
	session.enqueueSnapshot([]byte(`{"Type":"snapshot","Snapshot":{"Tick":7}}`))
	select {
	case <-conn.writes:
	case <-time.After(time.Second):
		t.Fatal("expected snapshot to be written before close")
	}
	deadline := time.Now().Add(time.Second)
	for {
		session.closeMetadataMu.Lock()
		lastSentTick := session.lastSentTick
		session.closeMetadataMu.Unlock()
		if lastSentTick == 7 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("last sent tick=%d, want 7", lastSentTick)
		}
		time.Sleep(time.Millisecond)
	}
	clock.Advance(3 * time.Second)

	const peerReason = "peer-close-reason-must-not-be-logged"
	recordWebSocketReadError(session, websocket.CloseError{Code: websocket.StatusPolicyViolation, Reason: peerReason})

	assertLogEventCount(t, logs, "websocket_disconnected", 1)
	record := matchingLogRecord(t, logs, "websocket_disconnected")
	if got := record["close_cause"]; got != "peer_close" {
		t.Fatalf("close cause=%v, want peer_close; logs=%s", got, logs.String())
	}
	if got := record["connection_generation"]; got != float64(1) {
		t.Fatalf("connection generation=%v, want 1; logs=%s", got, logs.String())
	}
	if got := record["match_phase"]; got != "started" {
		t.Fatalf("match phase=%v, want started; logs=%s", got, logs.String())
	}
	if got := record["last_sent_tick"]; got != float64(7) {
		t.Fatalf("last sent tick=%v, want 7; logs=%s", got, logs.String())
	}
	duration, ok := record["session_duration_ms"].(float64)
	if !ok || duration < 3000 {
		t.Fatalf("session duration=%v, want at least 3000ms; logs=%s", record["session_duration_ms"], logs.String())
	}
	for _, forbidden := range []string{issued.SessionToken, peerReason, "private=query"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("close log leaked %q: %s", forbidden, logs.String())
		}
	}

	metricsRecorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRecorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := metricsRecorder.Body.String()
	for _, fragment := range []string{
		"# TYPE crawlstars_websocket_closes_total counter",
		`crawlstars_websocket_closes_total{cause="peer_close"} 1`,
	} {
		if !strings.Contains(metricsBody, fragment) {
			t.Fatalf("metrics missing %q:\n%s", fragment, metricsBody)
		}
	}
	if strings.Contains(metricsBody, issued.Player.ID) || strings.Contains(metricsBody, created.ID) {
		t.Fatalf("close counter leaked room/player identifier:\n%s", metricsBody)
	}
}

func TestLateCloseOfOlderGenerationDoesNotAttributeToReconnect(t *testing.T) {
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

	allowOldClose := make(chan struct{})
	oldConn := newFakeClientConn(false)
	oldConn.closeBlock = allowOldClose
	oldConn.closeStarted = make(chan struct{})
	oldSession := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, oldConn)
	oldCloseDone := make(chan struct{})
	go func() {
		recordWebSocketReadError(oldSession, errors.New("old generation read failure"))
		close(oldCloseDone)
	}()
	select {
	case <-oldConn.closeStarted:
	case <-time.After(time.Second):
		close(allowOldClose)
		t.Fatal("expected old generation transport close")
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
	close(allowOldClose)
	<-oldCloseDone

	recordWebSocketReadError(currentSession, websocket.CloseError{Code: websocket.StatusPolicyViolation})
	records := websocketDisconnectedRecords(t, logs)
	if len(records) != 2 {
		t.Fatalf("disconnected records=%d, want 2; logs=%s", len(records), logs.String())
	}
	if records[0]["connection_generation"] != float64(1) || records[1]["connection_generation"] != float64(2) {
		t.Fatalf("generation records=%v, want [1 2]; logs=%s", records, logs.String())
	}
	if records[0]["close_cause"] != "read_failure" || records[1]["close_cause"] != "peer_close" {
		t.Fatalf("cause records=%v, want [read_failure peer_close]; logs=%s", records, logs.String())
	}
}

func TestWebSocketCloseObservationDistinguishesWriteTimeoutAndControlOverflow(t *testing.T) {
	t.Run("write timeout", func(t *testing.T) {
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
		conn := newFakeClientConn(false)
		conn.writeFn = func(context.Context, []byte) error { return context.DeadlineExceeded }
		session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)
		if !session.enqueueControl([]byte("write timeout")) {
			t.Fatal("expected write to enqueue")
		}
		<-session.closeDone
		record := matchingLogRecord(t, logs, "websocket_disconnected")
		if record["close_cause"] != "write_timeout" {
			t.Fatalf("close cause=%v, want write_timeout; logs=%s", record["close_cause"], logs.String())
		}
	})

	t.Run("control overflow", func(t *testing.T) {
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
		conn := newFakeClientConn(true)
		session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)
		if !session.enqueueControl([]byte("blocked write")) {
			t.Fatal("expected blocked write to enqueue")
		}
		select {
		case <-conn.writeStarted:
		case <-time.After(time.Second):
			t.Fatal("expected blocked write to start")
		}
		for range cap(session.control) {
			if !session.enqueueControl([]byte("queued control")) {
				t.Fatal("expected control queue to accept capacity")
			}
		}
		if session.enqueueControl([]byte("overflow")) {
			t.Fatal("expected control overflow to reject payload")
		}
		<-session.closeDone
		record := matchingLogRecord(t, logs, "websocket_disconnected")
		if record["close_cause"] != "control_overflow" {
			t.Fatalf("close cause=%v, want control_overflow; logs=%s", record["close_cause"], logs.String())
		}
	})
}

func TestWebSocketCloseCauseCatalogIsBoundedAndFirstCauseWins(t *testing.T) {
	causes := []websocketCloseCause{
		websocketCloseCausePeerClose,
		websocketCloseCauseReadFailure,
		websocketCloseCauseWriteTimeout,
		websocketCloseCauseWriteError,
		websocketCloseCausePingTimeout,
		websocketCloseCausePingError,
		websocketCloseCauseControlOverflow,
		websocketCloseCauseGameEnd,
		websocketCloseCausePrestartCancel,
		websocketCloseCauseExpiry,
		websocketCloseCauseShutdown,
		websocketCloseCauseDebugDelete,
	}
	for _, cause := range causes {
		t.Run(string(cause), func(t *testing.T) {
			if !isBoundedWebSocketCloseCause(cause) {
				t.Fatalf("cause %q is not bounded", cause)
			}
			session := newClientSession(newFakeClientConn(false), nil)
			session.closeWithCause(websocket.StatusGoingAway, "first", cause, "", "")
			session.closeWithCause(websocket.StatusGoingAway, "second", websocketCloseCauseReadFailure, "", "")
			if got := session.closeObservation().cause; got != cause {
				t.Fatalf("close cause=%q, want first cause %q", got, cause)
			}
		})
	}
	if isBoundedWebSocketCloseCause("unbounded") {
		t.Fatal("unexpected unbounded close cause accepted")
	}
}

func TestWebSocketLifecycleCloseCausesAreClaimedBeforeDisconnectPublication(t *testing.T) {
	t.Run("debug delete", func(t *testing.T) {
		logs, metrics, store, created, _ := attachedStartedSessionForCloseCause(t)
		if _, deleted := store.deleteRoom(created.ID); !deleted {
			t.Fatal("expected room deletion")
		}
		assertDisconnectedCloseCauses(t, logs, "debug_delete")
		assertWebSocketCloseMetric(t, metrics, "debug_delete", 1)
	})

	t.Run("expiry", func(t *testing.T) {
		logs, metrics, store, _, clock := attachedStartedSessionForCloseCause(t)
		clock.Advance(defaultHardRoomLifetime)
		if deleted := store.cleanupExpired(clock.Now()); deleted != 1 {
			t.Fatalf("expired rooms=%d, want 1", deleted)
		}
		assertDisconnectedCloseCauses(t, logs, "expiry")
		assertWebSocketCloseMetric(t, metrics, "expiry", 1)
	})

	t.Run("shutdown", func(t *testing.T) {
		logs, metrics, store, _, _ := attachedStartedSessionForCloseCause(t)
		if err := store.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown store: %v", err)
		}
		assertDisconnectedCloseCauses(t, logs, "shutdown")
		assertWebSocketCloseMetric(t, metrics, "shutdown", 1)
	})

	t.Run("prestart cancel", func(t *testing.T) {
		logs := &lockedLogBuffer{}
		metrics := observability.NewMetrics()
		store := newStore(5, newFakeClock(), StoreConfig{Logger: jsonTestLogger(logs), Observer: metrics})
		t.Cleanup(store.Close)
		first, err := store.joinMatchmaking(store.defaultGameMode())
		if err != nil {
			t.Fatalf("first matchmaking join: %v", err)
		}
		second, err := store.joinMatchmaking(store.defaultGameMode())
		if err != nil {
			t.Fatalf("second matchmaking join: %v", err)
		}
		firstReservation, err := store.reserveClient(first.Room.ID, first.Player.ID, []string{first.SessionToken})
		if err != nil {
			t.Fatalf("reserve first client: %v", err)
		}
		firstSession, attached := store.attachClientSession(firstReservation, newFakeClientConn(false))
		if !attached {
			t.Fatal("expected first client attach")
		}
		secondReservation, err := store.reserveClient(second.Room.ID, second.Player.ID, []string{second.SessionToken})
		if err != nil {
			t.Fatalf("reserve second client: %v", err)
		}
		if _, attached := store.attachClientSession(secondReservation, newFakeClientConn(false)); !attached {
			t.Fatal("expected second client attach")
		}

		firstSession.closeWithCause(websocket.StatusNormalClosure, "", websocketCloseCausePeerClose, "", "")

		assertDisconnectedCloseCauses(t, logs, "peer_close", "prestart_cancel")
		assertWebSocketCloseMetric(t, metrics, "peer_close", 1)
		assertWebSocketCloseMetric(t, metrics, "prestart_cancel", 1)
	})
}

func TestRealWebSocketHeartbeatControlFrames(t *testing.T) {
	t.Run("responsive idle client survives", func(t *testing.T) {
		logs := &lockedLogBuffer{}
		store := newStore(5, realClock{}, StoreConfig{
			Logger:            jsonTestLogger(logs),
			HeartbeatInterval: 10 * time.Millisecond,
			HeartbeatTimeout:  40 * time.Millisecond,
		})
		t.Cleanup(store.Close)
		handler := debugHandler(t, store)
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)

		room := createRoom(t, handler)
		issued := issuePlayer(t, handler, room.ID)
		startRoom(t, handler, room.ID)
		conn := dialIssuedPlayer(t, server.URL, issued.WebSocketPath)
		readCtx, cancelRead := context.WithCancel(context.Background())
		readErr := make(chan error, 1)
		go func() {
			for {
				if _, _, err := conn.Read(readCtx); err != nil {
					readErr <- err
					return
				}
			}
		}()
		t.Cleanup(func() {
			cancelRead()
			_ = conn.CloseNow()
		})
		waitForAttachedClient(t, store, room.ID, issued.ID)
		time.Sleep(80 * time.Millisecond)
		select {
		case err := <-readErr:
			t.Fatalf("responsive idle client closed early: %v; logs=%s", err, logs.String())
		default:
		}
	})

	t.Run("client that never reads times out", func(t *testing.T) {
		logs := &lockedLogBuffer{}
		store := newStore(5, realClock{}, StoreConfig{
			Logger:            jsonTestLogger(logs),
			HeartbeatInterval: 10 * time.Millisecond,
			HeartbeatTimeout:  40 * time.Millisecond,
		})
		t.Cleanup(store.Close)
		handler := debugHandler(t, store)
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)

		room := createRoom(t, handler)
		issued := issuePlayer(t, handler, room.ID)
		startRoom(t, handler, room.ID)
		conn := dialIssuedPlayer(t, server.URL, issued.WebSocketPath)
		t.Cleanup(func() { _ = conn.CloseNow() })
		waitForAttachedClient(t, store, room.ID, issued.ID)
		waitForDetachedClient(t, store, room.ID, issued.ID)
		record := waitForWebSocketDisconnectedRecord(t, logs)
		if got := record["close_cause"]; got != "ping_timeout" {
			t.Fatalf("close cause=%v, want ping_timeout; logs=%s", got, logs.String())
		}
	})
}

func waitForWebSocketDisconnectedRecord(t *testing.T, logs *lockedLogBuffer) map[string]any {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		records := websocketDisconnectedRecords(t, logs)
		if len(records) > 0 {
			return records[len(records)-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for websocket_disconnected; logs=%s", logs.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func attachedStartedSessionForCloseCause(t *testing.T) (*lockedLogBuffer, *observability.Metrics, *Store, roomResponse, *fakeClock) {
	t.Helper()
	logs := &lockedLogBuffer{}
	metrics := observability.NewMetrics()
	clock := newFakeClock()
	store := newStore(5, clock, StoreConfig{Logger: jsonTestLogger(logs), Observer: metrics})
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
	attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, newFakeClientConn(false))
	return logs, metrics, store, created, clock
}

func assertDisconnectedCloseCauses(t *testing.T, logs *lockedLogBuffer, want ...string) {
	t.Helper()
	records := websocketDisconnectedRecords(t, logs)
	got := make([]string, 0, len(records))
	for _, record := range records {
		cause, _ := record["close_cause"].(string)
		got = append(got, cause)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("close causes=%v, want %v; logs=%s", got, want, logs.String())
	}
}

func assertWebSocketCloseMetric(t *testing.T, metrics *observability.Metrics, cause string, want int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	fragment := fmt.Sprintf(`crawlstars_websocket_closes_total{cause=%q} %d`, cause, want)
	if !strings.Contains(recorder.Body.String(), fragment) {
		t.Fatalf("metrics missing %q:\n%s", fragment, recorder.Body.String())
	}
}

func websocketDisconnectedRecords(t *testing.T, logs *lockedLogBuffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if record["event"] == "websocket_disconnected" {
			records = append(records, record)
		}
	}
	return records
}
