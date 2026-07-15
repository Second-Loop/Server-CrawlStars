package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

const gameplayInterval = time.Second / time.Duration(simulation.TickRate)

type snapshotMessage struct {
	Type     string              `json:"Type"`
	Snapshot simulation.Snapshot `json:"Snapshot"`
}

type issuedPlayer struct {
	playerResponse
	SessionToken  string
	WebSocketPath string
}

type countingJSONMessage struct {
	calls *atomic.Int32
}

func (m countingJSONMessage) MarshalJSON() ([]byte, error) {
	m.calls.Add(1)
	return []byte(`{"Type":"snapshot"}`), nil
}

type fakeClientConn struct {
	writeStarted chan struct{}
	allowWrite   chan struct{}
	closed       chan struct{}
	writes       chan []byte
	events       chan string
	writeFn      func(context.Context, []byte) error
	pingFn       func(context.Context) error
	forceFn      func() error
	closeBlock   <-chan struct{}
	closeStarted chan struct{}
	writeOnce    sync.Once
	closeOnce    sync.Once
	closeMu      sync.Mutex
	closeCode    websocket.StatusCode
	closeReason  string
	closeCount   atomic.Int32
	forceCount   atomic.Int32
	pingCount    atomic.Int32
}

func newFakeClientConn(blockWrites bool) *fakeClientConn {
	conn := &fakeClientConn{
		writeStarted: make(chan struct{}),
		allowWrite:   make(chan struct{}),
		closed:       make(chan struct{}),
		writes:       make(chan []byte, 16),
	}
	if !blockWrites {
		close(conn.allowWrite)
	}
	return conn
}

func (c *fakeClientConn) Read(context.Context) (websocket.MessageType, []byte, error) {
	return 0, nil, errors.New("read not configured")
}

func (c *fakeClientConn) Write(ctx context.Context, _ websocket.MessageType, payload []byte) error {
	c.writeOnce.Do(func() { close(c.writeStarted) })
	if c.writeFn != nil {
		return c.writeFn(ctx, payload)
	}
	select {
	case <-c.allowWrite:
		copied := append([]byte(nil), payload...)
		c.writes <- copied
		if c.events != nil {
			c.events <- string(copied)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return errors.New("connection closed")
	}
}

func (c *fakeClientConn) Ping(ctx context.Context) error {
	c.pingCount.Add(1)
	if c.pingFn != nil {
		return c.pingFn(ctx)
	}
	return nil
}

func (c *fakeClientConn) Close(code websocket.StatusCode, reason string) error {
	c.closeCount.Add(1)
	c.closeMu.Lock()
	c.closeCode = code
	c.closeReason = reason
	c.closeMu.Unlock()
	c.closeOnce.Do(func() { close(c.closed) })
	if c.closeStarted != nil {
		close(c.closeStarted)
	}
	if c.closeBlock != nil {
		<-c.closeBlock
	}
	if c.events != nil {
		c.events <- "close"
	}
	return nil
}

func (c *fakeClientConn) closeMetadata() (websocket.StatusCode, string) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closeCode, c.closeReason
}

func (c *fakeClientConn) CloseNow() error {
	c.forceCount.Add(1)
	if c.forceFn != nil {
		return c.forceFn()
	}
	return nil
}

func TestHeartbeatResponsivePeerSurvivesRepeatedConfiguredTicks(t *testing.T) {
	fakeClock := newFakeClock()
	conn := newFakeClientConn(false)
	contexts := make(chan context.Context, 2)
	conn.pingFn = func(ctx context.Context) error {
		contexts <- ctx
		return nil
	}
	var released atomic.Int32
	session := newClientSession(conn, func(*clientSession) {
		released.Add(1)
	})
	session.startHeartbeat(fakeClock, 7*time.Second, 90*time.Second)
	t.Cleanup(func() {
		session.close(websocket.StatusNormalClosure, "test complete")
	})

	for range 2 {
		fakeClock.TickTicker(7*time.Second, 0)
		select {
		case ctx := <-contexts:
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("expected Ping context to have a deadline")
			}
			remaining := time.Until(deadline)
			if remaining < 89*time.Second || remaining > 90*time.Second {
				t.Fatalf("expected Ping deadline near 90s, got %s", remaining)
			}
		case <-time.After(time.Second):
			t.Fatal("expected heartbeat Ping")
		}
	}

	if got := conn.pingCount.Load(); got != 2 {
		t.Fatalf("expected two responsive Pings, got %d", got)
	}
	if session.isDone() {
		t.Fatal("expected responsive peer to remain connected")
	}
	if got := released.Load(); got != 0 {
		t.Fatalf("expected responsive peer not to release, got %d", got)
	}
}

func TestHeartbeatErrorAndBlockedTimeoutCloseReleaseExactlyOnce(t *testing.T) {
	tests := []struct {
		name   string
		pingFn func(context.Context) error
	}{
		{
			name: "ping error",
			pingFn: func(context.Context) error {
				return errors.New("ping failed")
			},
		},
		{
			name: "blocked ping timeout",
			pingFn: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := newFakeClock()
			conn := newFakeClientConn(false)
			conn.pingFn = tt.pingFn
			var released atomic.Int32
			var session *clientSession
			session = newClientSession(conn, func(expected *clientSession) {
				if expected != session {
					t.Error("expected heartbeat to release the current session")
				}
				released.Add(1)
			})
			session.startHeartbeat(fakeClock, time.Second, 20*time.Millisecond)

			fakeClock.TickTicker(time.Second, 0)
			select {
			case <-session.heartbeatDone:
			case <-time.After(time.Second):
				t.Fatal("expected failed heartbeat to exit")
			}

			if got := conn.closeCount.Load(); got != 1 {
				t.Fatalf("expected connection close once, got %d", got)
			}
			if got := released.Load(); got != 1 {
				t.Fatalf("expected release once, got %d", got)
			}
			session.close(websocket.StatusGoingAway, "repeated close")
			if got := conn.closeCount.Load(); got != 1 {
				t.Fatalf("expected repeated close to remain once, got %d", got)
			}
			if got := released.Load(); got != 1 {
				t.Fatalf("expected repeated release to remain once, got %d", got)
			}
		})
	}
}

func TestHeartbeatStoreConfigDefaultsAndOverrides(t *testing.T) {
	defaultStore := newStore(5, newFakeClock(), StoreConfig{})
	if defaultStore.heartbeatInterval != 30*time.Second || defaultStore.heartbeatTimeout != 90*time.Second {
		t.Fatalf("expected 30s/90s heartbeat defaults, got %s/%s", defaultStore.heartbeatInterval, defaultStore.heartbeatTimeout)
	}
	defaultStore.Close()

	overrideStore := newStore(5, newFakeClock(), StoreConfig{
		HeartbeatInterval: 7 * time.Second,
		HeartbeatTimeout:  20 * time.Millisecond,
	})
	if overrideStore.heartbeatInterval != 7*time.Second || overrideStore.heartbeatTimeout != 20*time.Millisecond {
		t.Fatalf("expected configured heartbeat, got %s/%s", overrideStore.heartbeatInterval, overrideStore.heartbeatTimeout)
	}
	overrideStore.Close()
}

func TestHeartbeatFailureCancelsPreStartMatchOnce(t *testing.T) {
	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{HeartbeatInterval: 7 * time.Second})
	t.Cleanup(store.Close)
	first, err := store.joinMatchmaking(store.defaultGameMode())
	if err != nil {
		t.Fatalf("join first player: %v", err)
	}
	second, err := store.joinMatchmaking(store.defaultGameMode())
	if err != nil {
		t.Fatalf("join second player: %v", err)
	}
	if first.Room.ID != second.Room.ID {
		t.Fatal("expected players to share pre-start match")
	}

	conn := newFakeClientConn(false)
	conn.pingFn = func(context.Context) error { return errors.New("silent peer") }
	session := attachHeartbeatTestSession(t, store, first.Room.ID, first.Player.ID, first.SessionToken, conn)
	fakeClock.TickTicker(7*time.Second, 0)
	select {
	case <-session.heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("expected failed heartbeat to exit")
	}

	if got := store.lookupRoom(first.Room.ID); got != nil {
		t.Fatal("expected heartbeat failure to cancel pre-start match")
	}
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected failed pre-start session to close once, got %d", got)
	}
}

func TestHeartbeatTimeoutStartsStartedRoomDisconnectedTTL(t *testing.T) {
	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{
		HeartbeatInterval: 7 * time.Second,
		HeartbeatTimeout:  20 * time.Millisecond,
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
	started, err := store.startRoom(created.ID)
	if err != nil {
		t.Fatalf("start room: %v", err)
	}

	conn := newFakeClientConn(false)
	conn.pingFn = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	session := attachHeartbeatTestSession(t, store, started.ID, issued.Player.ID, issued.SessionToken, conn)
	fakeClock.TickTicker(7*time.Second, 0)
	select {
	case <-session.heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("expected blocked heartbeat to time out")
	}

	room := store.lookupRoom(started.ID)
	if room == nil {
		t.Fatal("expected started room to remain during disconnected TTL")
	}
	room.mu.Lock()
	disconnectedAt := room.disconnectedAt
	connectedClients := len(room.clients)
	room.mu.Unlock()
	if !disconnectedAt.Equal(fakeClock.Now()) || connectedClients != 0 {
		t.Fatalf("expected disconnected TTL to start once, at=%s clients=%d", disconnectedAt, connectedClients)
	}

	fakeClock.Advance(defaultDisconnectedRoomTTL - time.Nanosecond)
	if deleted := store.cleanupExpired(fakeClock.Now()); deleted != 0 {
		t.Fatalf("expected room before disconnected TTL to remain, deleted=%d", deleted)
	}
	fakeClock.Advance(time.Nanosecond)
	if deleted := store.cleanupExpired(fakeClock.Now()); deleted != 1 {
		t.Fatalf("expected room at disconnected TTL to be removed, deleted=%d", deleted)
	}
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected timeout and cleanup to close session once, got %d", got)
	}
}

func TestHeartbeatStaleFailureDoesNotRemoveReconnect(t *testing.T) {
	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{HeartbeatInterval: 7 * time.Second})
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

	pingStarted := make(chan struct{})
	allowPingFailure := make(chan struct{})
	staleConn := newFakeClientConn(false)
	staleConn.pingFn = func(context.Context) error {
		close(pingStarted)
		<-allowPingFailure
		return errors.New("stale heartbeat failed")
	}
	staleSession := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, staleConn)
	fakeClock.TickTicker(7*time.Second, 0)
	select {
	case <-pingStarted:
	case <-time.After(time.Second):
		t.Fatal("expected stale heartbeat Ping to start")
	}

	currentSession := newClientSession(newFakeClientConn(false), nil)
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.clients[issued.Player.ID] = currentSession
	room.mu.Unlock()
	close(allowPingFailure)
	select {
	case <-staleSession.heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("expected stale heartbeat to exit")
	}

	room.mu.Lock()
	gotSession := room.clients[issued.Player.ID]
	disconnectedAt := room.disconnectedAt
	room.mu.Unlock()
	if gotSession != currentSession || !disconnectedAt.IsZero() {
		t.Fatal("expected stale heartbeat not to remove reconnect or start disconnected TTL")
	}
}

func TestHeartbeatFailureRacesManualReadAndWriteCloseExactlyOnce(t *testing.T) {
	fakeClock := newFakeClock()
	conn := newFakeClientConn(false)
	beginFailures := make(chan struct{})
	pingStarted := make(chan struct{})
	conn.pingFn = func(context.Context) error {
		close(pingStarted)
		<-beginFailures
		return errors.New("ping failed")
	}
	conn.writeFn = func(context.Context, []byte) error {
		<-beginFailures
		return errors.New("write failed")
	}
	var released atomic.Int32
	session := newClientSession(conn, func(*clientSession) { released.Add(1) })
	session.startHeartbeat(fakeClock, time.Second, time.Second)
	if !session.enqueueControl([]byte("control")) {
		t.Fatal("expected control write to enqueue")
	}
	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected writer to start")
	}
	fakeClock.TickTicker(time.Second, 0)
	select {
	case <-pingStarted:
	case <-time.After(time.Second):
		t.Fatal("expected heartbeat to start")
	}

	var closes sync.WaitGroup
	closes.Add(2)
	go func() {
		defer closes.Done()
		<-beginFailures
		session.close(websocket.StatusNormalClosure, "manual close")
	}()
	go func() {
		defer closes.Done()
		<-beginFailures
		session.close(websocket.StatusNormalClosure, "read failed")
	}()
	close(beginFailures)
	closes.Wait()
	select {
	case <-session.heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("expected heartbeat goroutine to exit")
	}
	select {
	case <-session.writerDone:
	case <-time.After(time.Second):
		t.Fatal("expected writer goroutine to exit")
	}
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected all close paths to close connection once, got %d", got)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("expected all close paths to release once, got %d", got)
	}
	if got := fakeClock.StopCount(); got != 1 {
		t.Fatalf("expected heartbeat ticker to stop once, got %d", got)
	}
}

func TestHeartbeatGoroutineAndTickerExitOnStoreClose(t *testing.T) {
	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{HeartbeatInterval: 7 * time.Second})
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	conn := newFakeClientConn(false)
	session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)

	store.Close()
	select {
	case <-session.heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("expected Store.Close to stop heartbeat goroutine")
	}
	select {
	case <-session.writerDone:
	case <-time.After(time.Second):
		t.Fatal("expected Store.Close to stop writer goroutine")
	}
	if got := fakeClock.StopCount(); got != 2 {
		t.Fatalf("expected janitor and heartbeat tickers to stop once, got %d", got)
	}
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected Store.Close to close session once, got %d", got)
	}
}

func attachHeartbeatTestSession(t *testing.T, store *Store, roomID string, playerID string, token string, conn clientConn) *clientSession {
	t.Helper()
	reservation, err := store.reserveClient(roomID, playerID, []string{token})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	session, attached := store.attachClientSession(reservation, conn)
	if !attached {
		t.Fatal("expected client session to attach")
	}
	return session
}

func TestClientOutboxSlowWriterDoesNotDelayFastClient(t *testing.T) {
	slowConn := newFakeClientConn(true)
	fastConn := newFakeClientConn(false)
	slowSession := newClientSession(slowConn, nil)
	fastSession := newClientSession(fastConn, nil)
	t.Cleanup(func() {
		slowSession.close(websocket.StatusNormalClosure, "test complete")
		fastSession.close(websocket.StatusNormalClosure, "test complete")
	})

	slowSession.enqueueSnapshot([]byte(`{"Tick":1}`))
	select {
	case <-slowConn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected slow client writer to start")
	}

	fastSession.enqueueSnapshot([]byte(`{"Tick":1}`))
	select {
	case payload := <-fastConn.writes:
		if string(payload) != `{"Tick":1}` {
			t.Fatalf("expected fast client snapshot, got %s", payload)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected blocked client writer not to delay fast client")
	}
}

func TestSlowClientWriterDoesNotDelayRoomTickOrFastClient(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	started := createStartedRoomInStore(t, store)
	room := store.lookupRoom(started.ID)
	slowConn := newFakeClientConn(true)
	fastConn := newFakeClientConn(false)
	slowSession := newClientSession(slowConn, nil)
	fastSession := newClientSession(fastConn, nil)

	room.mu.Lock()
	room.clients["slow"] = slowSession
	room.clients["fast"] = fastSession
	room.mu.Unlock()

	tickDone := make(chan struct{})
	go func() {
		store.tickRoom(started.ID)
		close(tickDone)
	}()
	select {
	case <-tickDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected room tick not to wait for a blocked client writer")
	}
	select {
	case <-fastConn.writes:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected fast client to receive tick snapshot")
	}
}

func TestClientOutboxSnapshotSlotKeepsNewestPayload(t *testing.T) {
	conn := newFakeClientConn(true)
	session := newClientSession(conn, nil)
	t.Cleanup(func() {
		session.close(websocket.StatusNormalClosure, "test complete")
	})

	session.enqueueSnapshot([]byte("first"))
	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected writer to take the first snapshot")
	}

	session.enqueueSnapshot([]byte("second"))
	session.enqueueSnapshot([]byte("newest"))
	close(conn.allowWrite)

	select {
	case payload := <-conn.writes:
		if string(payload) != "first" {
			t.Fatalf("expected in-flight first snapshot, got %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("expected first snapshot write")
	}
	select {
	case payload := <-conn.writes:
		if string(payload) != "newest" {
			t.Fatalf("expected newest queued snapshot, got %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("expected replacement snapshot write")
	}
}

func TestClientOutboxPreservesOrderedControlBeforeSnapshot(t *testing.T) {
	conn := newFakeClientConn(true)
	session := newClientSession(conn, nil)
	t.Cleanup(func() {
		session.close(websocket.StatusNormalClosure, "test complete")
	})

	session.enqueueSnapshot([]byte("in-flight"))
	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected writer to take the first snapshot")
	}

	for _, payload := range []string{"Ready", "starting", "error"} {
		if !session.enqueueControl([]byte(payload)) {
			t.Fatalf("expected %s control payload to enqueue", payload)
		}
	}
	session.enqueueSnapshot([]byte("gameplay"))
	close(conn.allowWrite)

	for _, want := range []string{"in-flight", "Ready", "starting", "error", "gameplay"} {
		select {
		case payload := <-conn.writes:
			if string(payload) != want {
				t.Fatalf("expected payload %q, got %q", want, payload)
			}
		case <-time.After(time.Second):
			t.Fatalf("expected payload %q", want)
		}
	}
}

func TestClientOutboxControlOverflowClosesAndReleasesCurrentSessionOnce(t *testing.T) {
	conn := newFakeClientConn(true)
	var released atomic.Int32
	var session *clientSession
	session = newClientSession(conn, func(expected *clientSession) {
		if expected != session {
			t.Errorf("expected release callback for current session")
		}
		released.Add(1)
	})

	session.enqueueSnapshot([]byte("in-flight"))
	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected writer to take the first snapshot")
	}

	for index := 0; index < 8; index++ {
		if !session.enqueueControl([]byte{byte(index)}) {
			t.Fatalf("expected control payload %d to fit size-8 queue", index)
		}
	}
	if session.enqueueControl([]byte("overflow")) {
		t.Fatal("expected ninth control payload to overflow")
	}
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected overflow to close connection once, got %d", got)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("expected overflow to release session once, got %d", got)
	}

	session.close(websocket.StatusGoingAway, "second close")
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected repeated close to preserve one connection close, got %d", got)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("expected repeated close to preserve one release, got %d", got)
	}
}

func TestClientOutboxWriteErrorOrTimeoutClosesAndReleasesCurrentSessionOnce(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "write error", err: errors.New("write failed")},
		{name: "write timeout", err: context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conn := newFakeClientConn(false)
			conn.writeFn = func(context.Context, []byte) error {
				return tt.err
			}
			released := make(chan *clientSession, 2)
			var session *clientSession
			session = newClientSession(conn, func(expected *clientSession) {
				released <- expected
			})

			if !session.enqueueControl([]byte("control")) {
				t.Fatal("expected control payload to enqueue")
			}
			select {
			case expected := <-released:
				if expected != session {
					t.Fatal("expected writer failure to release the same current session")
				}
			case <-time.After(time.Second):
				t.Fatal("expected writer failure to release session")
			}
			deadline := time.Now().Add(time.Second)
			for conn.closeCount.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if got := conn.closeCount.Load(); got != 1 {
				t.Fatalf("expected writer failure to close connection once, got %d", got)
			}

			session.close(websocket.StatusGoingAway, "repeated close")
			if got := conn.closeCount.Load(); got != 1 {
				t.Fatalf("expected repeated close to preserve one connection close, got %d", got)
			}
			select {
			case <-released:
				t.Fatal("expected repeated close not to release session again")
			case <-time.After(20 * time.Millisecond):
			}
		})
	}
}

func TestClientOutboxWriteErrorReleasesBeforeBlockingConnectionClose(t *testing.T) {
	conn := newFakeClientConn(false)
	conn.writeFn = func(context.Context, []byte) error {
		return errors.New("write failed")
	}
	allowClose := make(chan struct{})
	defer close(allowClose)
	conn.closeBlock = allowClose
	conn.closeStarted = make(chan struct{})
	released := make(chan struct{})
	session := newClientSession(conn, func(*clientSession) {
		close(released)
	})

	if !session.enqueueControl([]byte("control")) {
		t.Fatal("expected control payload to enqueue")
	}
	select {
	case <-conn.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected write error to start connection close")
	}
	select {
	case <-released:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected session release not to wait for blocking connection close")
	}
}

func TestClientOutboxUsesFreshFiveSecondContextForEveryWrite(t *testing.T) {
	conn := newFakeClientConn(false)
	contexts := make(chan context.Context, 2)
	conn.writeFn = func(ctx context.Context, _ []byte) error {
		contexts <- ctx
		return nil
	}
	session := newClientSession(conn, nil)
	t.Cleanup(func() {
		session.close(websocket.StatusNormalClosure, "test complete")
	})

	if !session.enqueueControl([]byte("first")) {
		t.Fatal("expected first control payload to enqueue")
	}
	first := <-contexts
	if !session.enqueueControl([]byte("second")) {
		t.Fatal("expected second control payload to enqueue")
	}
	second := <-contexts
	if first == second {
		t.Fatal("expected every write to receive a fresh context")
	}
	for index, ctx := range []context.Context{first, second} {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatalf("expected write context %d to have a deadline", index+1)
		}
		remaining := time.Until(deadline)
		if remaining < 4900*time.Millisecond || remaining > 5*time.Second {
			t.Fatalf("expected write context %d deadline near 5s, got %s", index+1, remaining)
		}
	}
}

func TestClientOutboxCloseCancelsInFlightWriteAndSkipsQueuedControl(t *testing.T) {
	conn := newFakeClientConn(false)
	firstWriteStarted := make(chan struct{})
	firstWriteReturned := make(chan struct{})
	secondWriteStarted := make(chan struct{}, 1)
	var writeCalls atomic.Int32
	conn.writeFn = func(ctx context.Context, _ []byte) error {
		if writeCalls.Add(1) == 1 {
			close(firstWriteStarted)
			<-ctx.Done()
			close(firstWriteReturned)
			// Model a writer that notices context cancellation but reports a
			// successful write. The session done gate must still prevent the
			// already queued control from reaching Conn.Write.
			return nil
		}
		secondWriteStarted <- struct{}{}
		return nil
	}
	session := newClientSession(conn, nil)

	if !session.enqueueControl([]byte("in-flight")) {
		t.Fatal("expected first control payload to enqueue")
	}
	select {
	case <-firstWriteStarted:
	case <-time.After(time.Second):
		t.Fatal("expected first write to start")
	}
	if !session.enqueueControl([]byte("must-not-write")) {
		t.Fatal("expected second control payload to enqueue before close")
	}

	session.close(websocket.StatusNormalClosure, "normal close")
	select {
	case <-firstWriteReturned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected session close to cancel an in-flight write that ignores Conn.Close")
	}
	select {
	case <-session.writerDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected writer to exit promptly after session close")
	}
	select {
	case <-secondWriteStarted:
		t.Fatal("expected no Conn.Write after session close")
	default:
	}
	if got := writeCalls.Load(); got != 1 {
		t.Fatalf("expected writer to exit after one write, got %d calls", got)
	}
}

func TestClientOutboxMarshalsNormalSnapshotOnce(t *testing.T) {
	firstConn := newFakeClientConn(false)
	secondConn := newFakeClientConn(false)
	first := newClientSession(firstConn, nil)
	second := newClientSession(secondConn, nil)
	t.Cleanup(func() {
		first.close(websocket.StatusNormalClosure, "test complete")
		second.close(websocket.StatusNormalClosure, "test complete")
	})
	var calls atomic.Int32

	if !enqueueSnapshotMessage([]*clientSession{first, second}, countingJSONMessage{calls: &calls}) {
		t.Fatal("expected snapshot message to enqueue")
	}
	for index, conn := range []*fakeClientConn{firstConn, secondConn} {
		select {
		case payload := <-conn.writes:
			if string(payload) != `{"Type":"snapshot"}` {
				t.Fatalf("expected client %d snapshot payload, got %s", index+1, payload)
			}
		case <-time.After(time.Second):
			t.Fatalf("expected client %d snapshot payload", index+1)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one normal snapshot marshal for fanout, got %d", got)
	}
}

func TestClientOutboxTerminalDiscardsSnapshotAndWritesPayloadsBeforeClose(t *testing.T) {
	conn := newFakeClientConn(true)
	conn.events = make(chan string, 8)
	session := newClientSession(conn, nil)

	if !session.enqueueControl([]byte("existing-control")) {
		t.Fatal("expected existing control payload to enqueue")
	}
	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected writer to take existing control")
	}
	session.enqueueSnapshot([]byte("stale-gameplay"))
	if !session.enqueueTerminal([]byte("terminal-snapshot"), []byte("GameEnd"), "game ended") {
		t.Fatal("expected terminal command sequence to enqueue")
	}
	if session.enqueueControl([]byte("after-terminal")) {
		t.Fatal("expected control payload after terminal to be rejected")
	}
	session.enqueueSnapshot([]byte("after-terminal"))
	close(conn.allowWrite)

	for _, want := range []string{"existing-control", "terminal-snapshot", "GameEnd", "close"} {
		select {
		case event := <-conn.events:
			if event != want {
				t.Fatalf("expected terminal event %q, got %q", want, event)
			}
		case <-time.After(time.Second):
			t.Fatalf("expected terminal event %q", want)
		}
	}
	select {
	case event := <-conn.events:
		t.Fatalf("expected no payload after terminal close, got %q", event)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestClientOutboxTerminalWaitsForEveryAcceptedControlWhenQueueIsNearlyOrCompletelyFull(t *testing.T) {
	for _, queuedControls := range []int{6, 8} {
		t.Run(fmt.Sprintf("%d of 8 controls queued", queuedControls), func(t *testing.T) {
			conn := newFakeClientConn(true)
			conn.events = make(chan string, 16)
			session := newClientSession(conn, nil)
			releasedWriter := false
			t.Cleanup(func() {
				if !releasedWriter {
					close(conn.allowWrite)
				}
				session.close(websocket.StatusNormalClosure, "test complete")
			})

			session.enqueueSnapshot([]byte("in-flight"))
			select {
			case <-conn.writeStarted:
			case <-time.After(time.Second):
				t.Fatal("expected writer to take the first snapshot")
			}

			for index := 0; index < queuedControls; index++ {
				payload := fmt.Sprintf("control-%d", index)
				if !session.enqueueControl([]byte(payload)) {
					t.Fatalf("expected %s to fit the regular control queue", payload)
				}
			}
			if !session.enqueueTerminal([]byte("terminal-snapshot"), []byte("GameEnd"), "game ended") {
				t.Fatal("expected terminal handoff not to depend on regular control queue capacity")
			}

			close(conn.allowWrite)
			releasedWriter = true
			want := []string{"in-flight"}
			for index := 0; index < queuedControls; index++ {
				want = append(want, fmt.Sprintf("control-%d", index))
			}
			want = append(want, "terminal-snapshot", "GameEnd", "close")
			for _, expected := range want {
				select {
				case event := <-conn.events:
					if event != expected {
						t.Fatalf("expected terminal event %q, got %q", expected, event)
					}
				case <-time.After(time.Second):
					t.Fatalf("expected terminal event %q", expected)
				}
			}
		})
	}
}

func TestTerminalDeliveryDoesNotLetRoomResourcesCloseRaceWriter(t *testing.T) {
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
	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("start room: %v", err)
	}

	conn := newFakeClientConn(true)
	conn.events = make(chan string, 4)
	session := newClientSession(conn, nil)
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.clients[issued.Player.ID] = session
	room.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
		return simulation.Snapshot{
			Tick: 1,
			Players: []simulation.PlayerData{{
				ID:     simulation.PlayerID(issued.Player.ID),
				HP:     0,
				IsDead: true,
			}},
		}
	})
	room.mu.Unlock()

	store.tickRoom(created.ID)
	if got := conn.closeCount.Load(); got != 0 {
		t.Fatalf("expected room resource cleanup not to close terminal writer early, got %d closes", got)
	}
	close(conn.allowWrite)

	var types []string
	for range 2 {
		select {
		case event := <-conn.events:
			var envelope struct {
				Type string `json:"Type"`
			}
			if err := json.Unmarshal([]byte(event), &envelope); err != nil {
				t.Fatalf("decode terminal payload %q: %v", event, err)
			}
			types = append(types, envelope.Type)
		case <-time.After(time.Second):
			t.Fatal("expected terminal payload")
		}
	}
	if got, want := strings.Join(types, ","), "snapshot,GameEnd"; got != want {
		t.Fatalf("expected terminal payload order %s, got %s", want, got)
	}
	select {
	case event := <-conn.events:
		if event != "close" {
			t.Fatalf("expected close after terminal payloads, got %q", event)
		}
	case <-time.After(time.Second):
		t.Fatal("expected terminal close")
	}
}

func TestStoreCloseStopsTerminalSessionAfterRoomLeavesRegistry(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
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
	t.Cleanup(func() {
		close(conn.allowWrite)
		session.close(websocket.StatusNormalClosure, "test cleanup")
		store.Close()
	})

	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
		return simulation.Snapshot{
			Tick: 1,
			Players: []simulation.PlayerData{{
				ID:     simulation.PlayerID(issued.Player.ID),
				HP:     0,
				IsDead: true,
			}},
		}
	})
	room.mu.Unlock()

	store.tickRoom(created.ID)
	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected terminal snapshot write to start")
	}
	if got := store.lookupRoom(created.ID); got != nil {
		t.Fatal("expected terminal room to leave the Store registry before shutdown")
	}

	store.Close()
	select {
	case <-session.done:
	default:
		t.Fatal("expected Store.Close to close terminal session after room deletion")
	}
	select {
	case <-session.writerDone:
	default:
		t.Fatal("expected Store.Close to wait for terminal writer exit")
	}
	select {
	case <-session.heartbeatDone:
	default:
		t.Fatal("expected Store.Close to wait for terminal heartbeat exit")
	}
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected Store.Close to close terminal connection once, got %d", got)
	}
}

func TestStoreCloseWaitsForTerminalSessionAlreadyBlockingInConnectionClose(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
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

	allowClose := make(chan struct{})
	var releaseClose sync.Once
	releaseBlockedClose := func() {
		releaseClose.Do(func() { close(allowClose) })
	}
	conn := newFakeClientConn(false)
	conn.closeBlock = allowClose
	conn.closeStarted = make(chan struct{})
	session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)
	t.Cleanup(func() {
		releaseBlockedClose()
		session.close(websocket.StatusNormalClosure, "test cleanup")
		store.Close()
	})

	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
		return simulation.Snapshot{
			Tick: 1,
			Players: []simulation.PlayerData{{
				ID:     simulation.PlayerID(issued.Player.ID),
				HP:     0,
				IsDead: true,
			}},
		}
	})
	room.mu.Unlock()

	store.tickRoom(created.ID)
	select {
	case <-conn.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected terminal writer to enter connection close")
	}
	if got := store.lookupRoom(created.ID); got != nil {
		t.Fatal("expected terminal room to leave the Store registry")
	}

	storeCloseDone := make(chan struct{})
	go func() {
		store.Close()
		close(storeCloseDone)
	}()
	select {
	case <-storeCloseDone:
		t.Fatal("expected Store.Close to wait for terminal connection close")
	case <-time.After(100 * time.Millisecond):
	}

	releaseBlockedClose()
	select {
	case <-storeCloseDone:
	case <-time.After(time.Second):
		t.Fatal("expected Store.Close to finish after terminal connection close")
	}
	select {
	case <-session.writerDone:
	default:
		t.Fatal("expected terminal writer to finish before Store.Close returns")
	}
	select {
	case <-session.heartbeatDone:
	default:
		t.Fatal("expected terminal heartbeat to finish before Store.Close returns")
	}
	store.mu.RLock()
	activeCount := len(store.activeSessions)
	store.mu.RUnlock()
	if activeCount != 0 {
		t.Fatalf("expected terminated session to leave active registry, got %d", activeCount)
	}
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected terminal connection close once, got %d", got)
	}
}

func TestTerminalSnapshotMarshalFailureClosesWithoutWritingSnapshotOrGameEnd(t *testing.T) {
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
	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("start room: %v", err)
	}

	conn := newFakeClientConn(false)
	session := newClientSession(conn, nil)
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.clients[issued.Player.ID] = session
	room.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
		return simulation.Snapshot{
			Tick: 1,
			Players: []simulation.PlayerData{{
				ID:     simulation.PlayerID(issued.Player.ID),
				HP:     math.NaN(),
				IsDead: true,
			}},
		}
	})
	room.mu.Unlock()

	store.tickRoom(created.ID)
	select {
	case <-session.writerDone:
	case <-time.After(time.Second):
		t.Fatal("expected marshal failure to close the terminal session")
	}
	select {
	case payload := <-conn.writes:
		t.Fatalf("expected no snapshot or GameEnd write after terminal marshal failure, got %q", payload)
	default:
	}
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected marshal failure to close connection once, got %d", got)
	}
}

func TestWebSocketControlOrderStartingBeforeCountdownCompletes(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	first, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add first player: %v", err)
	}
	second, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add second player: %v", err)
	}

	conn := newFakeClientConn(false)
	session := newClientSession(conn, nil)
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.matchStatus = MatchStatusLoading
	room.readyPlayers = make(map[string]bool)
	room.readyPlayers[first.Player.ID] = true
	room.clients[second.Player.ID] = session
	room.mu.Unlock()

	session.enqueueMu.Lock()
	barrierLocked := true
	defer func() {
		if barrierLocked {
			session.enqueueMu.Unlock()
		}
	}()
	readyDone := make(chan struct{})
	go func() {
		store.markClientReady(created.ID, second.Player.ID, session)
		close(readyDone)
	}()
	if !waitForFakeTickerCount(fakeClock, time.Second, 1, time.Second) {
		t.Fatal("expected ready transition to create countdown ticker")
	}
	for range matchCountdownSeconds {
		fakeClock.TickTicker(time.Second, 0)
	}

	gameplayInterval := time.Second / time.Duration(store.gameConfig.TickRate)
	prematureStart := waitForFakeTickerCount(fakeClock, gameplayInterval, 1, 100*time.Millisecond)
	session.enqueueMu.Unlock()
	barrierLocked = false
	select {
	case <-readyDone:
	case <-time.After(time.Second):
		t.Fatal("expected ready transition to finish after enqueue barrier release")
	}
	if prematureStart {
		t.Fatal("expected countdown not to complete before starting control enqueue")
	}
	if !waitForFakeTickerCount(fakeClock, gameplayInterval, 1, time.Second) {
		t.Fatal("expected countdown to complete after starting control enqueue")
	}

	starting := readFakeMatchSnapshot(t, conn)
	started := readFakeMatchSnapshot(t, conn)
	if starting.Snapshot.Status != string(MatchStatusStarting) || starting.Snapshot.Tick != 0 {
		t.Fatalf("expected starting control first, got %+v", starting.Snapshot)
	}
	if started.Snapshot.Status != string(MatchStatusStarted) || started.Snapshot.Tick != 0 {
		t.Fatalf("expected started control second, got %+v", started.Snapshot)
	}
}

func TestWebSocketControlOrderStartedBeforeGameplayTick(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}

	conn := newFakeClientConn(false)
	session := newClientSession(conn, nil)
	countdownTicker := newCountingTicker()
	stepped := make(chan struct{})
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.matchStatus = MatchStatusStarting
	room.countdown = 1
	room.countdownTicker = countdownTicker
	room.countdownStop = make(chan struct{})
	room.clients[issued.Player.ID] = session
	room.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
		close(stepped)
		return simulation.Snapshot{Tick: 1}
	})
	room.mu.Unlock()

	session.enqueueMu.Lock()
	barrierLocked := true
	defer func() {
		if barrierLocked {
			session.enqueueMu.Unlock()
		}
	}()
	countdownDone := make(chan struct{})
	go func() {
		store.tickMatchCountdownRoom(room, countdownTicker)
		close(countdownDone)
	}()
	gameplayInterval := time.Second / time.Duration(store.gameConfig.TickRate)
	if !waitForFakeTickerCount(fakeClock, gameplayInterval, 1, time.Second) {
		t.Fatal("expected final countdown transition to create gameplay ticker")
	}
	fakeClock.TickTicker(gameplayInterval, 0)
	select {
	case <-stepped:
		session.enqueueMu.Unlock()
		barrierLocked = false
		t.Fatal("expected gameplay tick not to run before started control enqueue")
	case <-time.After(100 * time.Millisecond):
	}
	session.enqueueMu.Unlock()
	barrierLocked = false
	select {
	case <-countdownDone:
	case <-time.After(time.Second):
		t.Fatal("expected final countdown transition to finish")
	}
	select {
	case <-stepped:
	case <-time.After(time.Second):
		t.Fatal("expected gameplay tick after started control enqueue")
	}

	started := readFakeMatchSnapshot(t, conn)
	gameplay := readFakeMatchSnapshot(t, conn)
	if started.Snapshot.Status != string(MatchStatusStarted) || started.Snapshot.Tick != 0 {
		t.Fatalf("expected started control first, got %+v", started.Snapshot)
	}
	if gameplay.Snapshot.Status != string(MatchStatusStarted) || gameplay.Snapshot.Tick != 1 {
		t.Fatalf("expected gameplay snapshot second, got %+v", gameplay.Snapshot)
	}
}

func TestWebSocketTokenRejectsInvalidCredentials(t *testing.T) {
	tests := []struct {
		name string
		path func(issuedPlayer, issuedPlayer) string
	}{
		{
			name: "missing",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return strings.SplitN(first.WebSocketPath, "?", 2)[0]
			},
		},
		{
			name: "empty",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return strings.SplitN(first.WebSocketPath, "?", 2)[0] + "?token="
			},
		},
		{
			name: "wrong",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return strings.SplitN(first.WebSocketPath, "?", 2)[0] + "?token=wrong"
			},
		},
		{
			name: "another player",
			path: func(first issuedPlayer, second issuedPlayer) string {
				return strings.SplitN(first.WebSocketPath, "?", 2)[0] + "?token=" + second.SessionToken
			},
		},
		{
			name: "multiple",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return first.WebSocketPath + "&token=extra"
			},
		},
		{
			name: "malformed query pair",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return first.WebSocketPath + "&bad=one;two"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStoreWithClock(5, newFakeClock())
			handler := debugHandler(t, store)
			server := httptest.NewServer(handler)
			defer server.Close()
			defer store.Close()

			room := createRoom(t, handler)
			first := issuePlayer(t, handler, room.ID)
			second := issuePlayer(t, handler, room.ID)

			assertWebSocketDialError(t, server.URL, tt.path(first, second), http.StatusUnauthorized, "unauthorized")
		})
	}
}

func TestWebSocketTokenPreservesUnknownIdentityErrors(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	assertWebSocketDialError(t, server.URL, "/rooms/missing/players/player_missing", http.StatusNotFound, "room_not_found")
	assertWebSocketDialError(t, server.URL, "/rooms/"+room.ID+"/players/player_missing", http.StatusNotFound, "player_not_found")
}

func TestWebSocketTokenAllowsCorrectConnectionAndReconnect(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	issued := issuePlayer(t, handler, room.ID)

	first := dialIssuedPlayer(t, server.URL, issued.WebSocketPath)
	waitForAttachedClient(t, store, room.ID, issued.ID)
	_ = first.Close(websocket.StatusNormalClosure, "")
	waitForDetachedClient(t, store, room.ID, issued.ID)

	second := dialIssuedPlayer(t, server.URL, issued.WebSocketPath)
	defer second.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, issued.ID)
}

func TestWebSocketTokenAuthenticationPrecedesDuplicateCheck(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	issued := issuePlayer(t, handler, room.ID)
	pathWithoutToken := strings.SplitN(issued.WebSocketPath, "?", 2)[0]

	first := dialIssuedPlayer(t, server.URL, issued.WebSocketPath)
	defer first.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, issued.ID)

	assertWebSocketDialError(t, server.URL, pathWithoutToken+"?token=wrong", http.StatusUnauthorized, "unauthorized")
	assertWebSocketDialError(t, server.URL, issued.WebSocketPath, http.StatusConflict, "player_already_connected")
}

func TestWebSocketFailedUpgradeRollsBackReservationWithoutCancelingMatch(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	defer store.Close()

	first := joinMatchmaking(t, handler)
	_ = joinMatchmaking(t, handler)

	rec := request(handler, http.MethodGet, first.WebSocketPath)
	if rec.Code == http.StatusSwitchingProtocols {
		t.Fatal("expected a non-WebSocket request to fail the upgrade")
	}

	matched := store.lookupRoom(first.Room.ID)
	roomExists := matched != nil
	clientCount := 0
	reservationCount := 0
	matchStatus := MatchStatus("")
	if matched != nil {
		matched.mu.Lock()
		clientCount = len(matched.clients)
		reservationCount = len(matched.reservations)
		matchStatus = matched.matchStatus
		matched.mu.Unlock()
	}

	if !roomExists || matchStatus != MatchStatusMatched {
		t.Fatal("expected failed upgrade to preserve the matched room")
	}
	if clientCount != 0 || reservationCount != 0 {
		t.Fatal("expected failed upgrade to leave no client or reservation")
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	conn := dialIssuedPlayer(t, server.URL, first.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, first.Room.ID, first.Player.ID)
}

func TestClientReservationCannotAttachAfterRoomRemoval(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	defer store.Close()
	handler := debugHandler(t, store)
	room := createRoom(t, handler)
	issued := issuePlayer(t, handler, room.ID)

	reservation, err := store.reserveClient(room.ID, issued.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	if _, ok := store.deleteRoom(room.ID); !ok {
		t.Fatal("expected room deletion to succeed")
	}
	if store.attachClient(reservation, nil) {
		t.Fatal("expected attachment to fail after room deletion")
	}
	store.rollbackClientReservation(reservation)
}

func TestClientReservationCannotAttachAfterStoreClose(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	room := createRoom(t, handler)
	issued := issuePlayer(t, handler, room.ID)

	reservation, err := store.reserveClient(room.ID, issued.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	store.Close()
	if store.attachClient(reservation, nil) {
		t.Fatal("expected attachment to fail after store close")
	}
	store.rollbackClientReservation(reservation)
}

func TestClientAttachReleasesStoreLockBeforeRoomStateWork(t *testing.T) {
	baseClock := newFakeClock()
	clock := &blockingNextNowClock{
		base:       baseClock,
		nowStarted: make(chan struct{}),
		allowNow:   make(chan struct{}),
	}
	store := NewStoreWithClock(5, clock)
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

	clock.blockNext.Store(true)
	attachDone := make(chan struct{})
	var session *clientSession
	var attached bool
	go func() {
		session, attached = store.attachClientSession(reservation, newFakeClientConn(false))
		close(attachDone)
	}()
	select {
	case <-clock.nowStarted:
	case <-time.After(time.Second):
		close(clock.allowNow)
		<-attachDone
		store.Close()
		t.Fatal("expected attach to reach room activity update")
	}

	storeLockAvailable := store.mu.TryRLock()
	if storeLockAvailable {
		store.mu.RUnlock()
	}
	close(clock.allowNow)
	select {
	case <-attachDone:
	case <-time.After(time.Second):
		store.Close()
		t.Fatal("expected attach to finish after room activity update resumes")
	}
	store.Close()
	if !attached || session == nil {
		t.Fatal("expected reserved client to attach")
	}
	if !storeLockAvailable {
		t.Fatal("expected attach to release Store.mu before room-only state work")
	}
}

func TestStaleSessionReaderReleasePreservesReplacementRoomSession(t *testing.T) {
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
	staleConn := newFakeClientConn(false)
	staleSession, attached := store.attachClientSession(reservation, staleConn)
	if !attached {
		t.Fatal("expected stale connection to attach before replacement")
	}

	original := reservation.room
	var resources roomResources
	original.mu.Lock()
	_, removed := resources.removeRoomLocked(original)
	original.mu.Unlock()
	if !removed {
		t.Fatal("expected original room to be marked removed")
	}

	currentSession := newClientSession(newFakeClientConn(false), nil)
	t.Cleanup(func() {
		staleSession.close(websocket.StatusNormalClosure, "test complete")
		currentSession.close(websocket.StatusNormalClosure, "test complete")
	})
	store.mu.Lock()
	replacement := store.newRoomLocked(created.ID, store.gameConfig)
	replacement.Players = append(replacement.Players, issued.Player)
	replacement.clients[issued.Player.ID] = currentSession
	store.rooms[created.ID] = replacement
	store.mu.Unlock()

	staleSession.close(websocket.StatusNormalClosure, "stale reader closed")

	if got := store.lookupRoom(created.ID); got != replacement {
		t.Fatal("expected stale release not to delete the replacement room")
	}
	replacement.mu.Lock()
	gotSession, connected := replacement.clients[issued.Player.ID]
	delete(replacement.clients, issued.Player.ID)
	replacement.mu.Unlock()
	store.Close()
	if !connected || gotSession != currentSession {
		t.Fatal("expected stale release not to detach the replacement room connection")
	}
}

func TestStaleSessionReaderReleaseDoesNotRemoveReconnect(t *testing.T) {
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
	staleSession, attached := store.attachClientSession(reservation, newFakeClientConn(false))
	if !attached {
		t.Fatal("expected stale connection to attach")
	}

	currentSession := newClientSession(newFakeClientConn(false), nil)
	t.Cleanup(func() {
		staleSession.close(websocket.StatusNormalClosure, "test complete")
		currentSession.close(websocket.StatusNormalClosure, "test complete")
	})
	room := reservation.room
	room.mu.Lock()
	room.clients[issued.Player.ID] = currentSession
	room.mu.Unlock()

	staleSession.close(websocket.StatusNormalClosure, "stale reader closed")

	room.mu.Lock()
	gotSession, connected := room.clients[issued.Player.ID]
	delete(room.clients, issued.Player.ID)
	room.mu.Unlock()
	store.Close()
	if !connected || gotSession != currentSession {
		t.Fatal("expected stale release not to detach the current connection")
	}
}

func TestStaleSessionWriterFailureDoesNotRemoveReconnect(t *testing.T) {
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
	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("start room: %v", err)
	}
	reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}

	releaseWrite := make(chan struct{})
	staleConn := newFakeClientConn(false)
	staleConn.writeFn = func(context.Context, []byte) error {
		<-releaseWrite
		return errors.New("stale writer failed")
	}
	staleSession, attached := store.attachClientSession(reservation, staleConn)
	if !attached {
		t.Fatal("expected stale session to attach")
	}
	if !staleSession.enqueueControl([]byte("control")) {
		t.Fatal("expected stale writer control to enqueue")
	}
	select {
	case <-staleConn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("expected stale writer to start")
	}

	currentSession := newClientSession(newFakeClientConn(false), nil)
	room := reservation.room
	room.mu.Lock()
	room.clients[issued.Player.ID] = currentSession
	room.mu.Unlock()
	close(releaseWrite)

	deadline := time.Now().Add(time.Second)
	for staleConn.closeCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := staleConn.closeCount.Load(); got != 1 {
		t.Fatalf("expected stale writer failure to close old connection once, got %d", got)
	}
	room.mu.Lock()
	gotSession, connected := room.clients[issued.Player.ID]
	room.mu.Unlock()
	if !connected || gotSession != currentSession {
		t.Fatal("expected stale writer failure not to remove reconnected current session")
	}
}

func TestStaleSessionPayloadCannotMutateReconnectWhileOldCloseBlocks(t *testing.T) {
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
	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("start room: %v", err)
	}
	reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve old session: %v", err)
	}

	allowOldClose := make(chan struct{})
	oldConn := newFakeClientConn(false)
	oldConn.closeBlock = allowOldClose
	oldConn.closeStarted = make(chan struct{})
	oldSession, attached := store.attachClientSession(reservation, oldConn)
	if !attached {
		t.Fatal("expected old session to attach")
	}
	oldCloseDone := make(chan struct{})
	go func() {
		oldSession.close(websocket.StatusNormalClosure, "old reader closed")
		close(oldCloseDone)
	}()
	select {
	case <-oldConn.closeStarted:
	case <-time.After(time.Second):
		close(allowOldClose)
		t.Fatal("expected old connection close to block after release")
	}

	reconnectReservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
	if err != nil {
		close(allowOldClose)
		t.Fatalf("reserve reconnect: %v", err)
	}
	currentSession, attached := store.attachClientSession(reconnectReservation, newFakeClientConn(false))
	if !attached {
		close(allowOldClose)
		t.Fatal("expected reconnect session to attach")
	}

	store.setInput(created.ID, issued.Player.ID, inputMessage{MoveDir: simulation.Vector2{X: 1}}, oldSession)
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	_, staleInputApplied := room.pendingInputs[issued.Player.ID]
	room.Status = RoomStatusWaiting
	room.matchStatus = MatchStatusLoading
	room.readyPlayers = make(map[string]bool)
	room.mu.Unlock()
	store.markClientReady(created.ID, issued.Player.ID, oldSession)

	room.mu.Lock()
	current := room.clients[issued.Player.ID]
	staleReadyApplied := room.readyPlayers[issued.Player.ID]
	status := room.matchStatus
	room.mu.Unlock()
	close(allowOldClose)
	select {
	case <-oldCloseDone:
	case <-time.After(time.Second):
		t.Fatal("expected old connection close to finish")
	}

	if staleInputApplied {
		t.Fatal("expected stale session input not to mutate reconnected room")
	}
	if staleReadyApplied || status != MatchStatusLoading {
		t.Fatalf("expected stale ready payload not to advance reconnect, ready=%t status=%q", staleReadyApplied, status)
	}
	if current != currentSession {
		t.Fatal("expected stale payloads not to replace current session")
	}
}

func TestClientReservationRollbackRestoresDisconnectedAt(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	defer store.Close()
	handler := debugHandler(t, store)
	roomResponse := createRoom(t, handler)
	issued := issuePlayer(t, handler, roomResponse.ID)
	previousDisconnectedAt := fakeClock.Now().Add(-time.Minute)

	room := store.lookupRoom(roomResponse.ID)
	room.mu.Lock()
	room.Status = RoomStatusStarted
	room.disconnectedAt = previousDisconnectedAt
	room.mu.Unlock()

	reservation, err := store.reserveClient(roomResponse.ID, issued.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	room.mu.Lock()
	reservedDisconnectedAt := room.disconnectedAt
	room.mu.Unlock()
	if !reservedDisconnectedAt.Equal(previousDisconnectedAt) {
		t.Fatal("expected reservation to preserve the disconnected timestamp")
	}
	store.rollbackClientReservation(reservation)

	room.mu.Lock()
	gotDisconnectedAt := room.disconnectedAt
	reservationCount := len(room.reservations)
	room.mu.Unlock()
	if !gotDisconnectedAt.Equal(previousDisconnectedAt) {
		t.Fatal("expected rollback to restore the disconnected timestamp")
	}
	if reservationCount != 0 {
		t.Fatal("expected rollback to remove the reservation")
	}
}

func TestClientReservationRollbackPreservesActivityAcrossOrders(t *testing.T) {
	tests := []struct {
		name  string
		order []int
	}{
		{name: "first then second", order: []int{0, 1}},
		{name: "second then first", order: []int{1, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := newFakeClock()
			store := NewStoreWithClock(5, fakeClock)
			defer store.Close()
			handler := debugHandler(t, store)
			roomResponse := createRoom(t, handler)
			first := issuePlayer(t, handler, roomResponse.ID)
			second := issuePlayer(t, handler, roomResponse.ID)

			room := store.lookupRoom(roomResponse.ID)
			room.mu.Lock()
			originalLastActivityAt := fakeClock.Now()
			room.lastActivityAt = originalLastActivityAt
			room.mu.Unlock()

			fakeClock.Advance(time.Minute)
			firstReservation, err := store.reserveClient(roomResponse.ID, first.ID, []string{first.SessionToken})
			if err != nil {
				t.Fatalf("reserve first client: %v", err)
			}
			fakeClock.Advance(time.Minute)
			secondReservation, err := store.reserveClient(roomResponse.ID, second.ID, []string{second.SessionToken})
			if err != nil {
				t.Fatalf("reserve second client: %v", err)
			}

			room.mu.Lock()
			gotAfterReservations := room.lastActivityAt
			room.mu.Unlock()
			if !gotAfterReservations.Equal(originalLastActivityAt) {
				t.Fatal("expected reservations not to count as room activity")
			}

			reservations := []*clientReservation{firstReservation, secondReservation}
			for _, index := range tt.order {
				store.rollbackClientReservation(reservations[index])
			}

			room.mu.Lock()
			gotLastActivityAt := room.lastActivityAt
			reservationCount := len(room.reservations)
			room.mu.Unlock()
			if !gotLastActivityAt.Equal(originalLastActivityAt) {
				t.Fatal("expected rollback order not to change room activity")
			}
			if reservationCount != 0 {
				t.Fatalf("expected no reservations, got %d", reservationCount)
			}
		})
	}
}

func TestClientReservationAttachAndRollbackSameTickKeepsAttachActivity(t *testing.T) {
	tests := []struct {
		name          string
		attachedIndex int
		rollbackIndex int
	}{
		{name: "attach first rollback second", attachedIndex: 0, rollbackIndex: 1},
		{name: "attach second rollback first", attachedIndex: 1, rollbackIndex: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := newFakeClock()
			store := NewStoreWithClock(5, fakeClock)
			defer store.Close()
			handler := debugHandler(t, store)
			roomResponse := createRoom(t, handler)
			players := []issuedPlayer{
				issuePlayer(t, handler, roomResponse.ID),
				issuePlayer(t, handler, roomResponse.ID),
			}

			room := store.lookupRoom(roomResponse.ID)
			room.mu.Lock()
			room.lastActivityAt = fakeClock.Now().Add(-time.Minute)
			room.mu.Unlock()

			reservations := make([]*clientReservation, len(players))
			for index, player := range players {
				reservation, err := store.reserveClient(roomResponse.ID, player.ID, []string{player.SessionToken})
				if err != nil {
					t.Fatalf("reserve client %d: %v", index, err)
				}
				reservations[index] = reservation
			}
			if !store.attachClient(reservations[tt.attachedIndex], nil) {
				t.Fatal("expected reservation to attach")
			}
			store.rollbackClientReservation(reservations[tt.rollbackIndex])

			room.mu.Lock()
			gotLastActivityAt := room.lastActivityAt
			reservationCount := len(room.reservations)
			room.mu.Unlock()
			if !gotLastActivityAt.Equal(fakeClock.Now()) {
				t.Fatal("expected successful attachment to remain the latest room activity")
			}
			if reservationCount != 0 {
				t.Fatalf("expected no reservations, got %d", reservationCount)
			}
		})
	}
}

func TestWebSocketConnectsIssuedPlayerAndBroadcastsSnapshotsOnTicks(t *testing.T) {
	fakeClock := newFakeClock()
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

	fakeClock.TickTicker(gameplayInterval, 0)
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

	fakeClock.TickTicker(gameplayInterval, 0)
	second := readSnapshotMessage(t, conn)
	if second.Snapshot.Tick != 2 {
		t.Fatalf("expected second snapshot tick 2, got %d", second.Snapshot.Tick)
	}
	if got := fakeClock.TickerCount(gameplayInterval); got != 1 {
		t.Fatalf("expected one 30Hz gameplay ticker, got %d", got)
	}
}

func TestWebSocketWriterUsesFiveSecondWriteTimeout(t *testing.T) {
	if webSocketWriteTimeout != 5*time.Second {
		t.Fatalf("expected websocket writer timeout 5s, got %s", webSocketWriteTimeout)
	}
}

func TestWebSocketRejectsUnknownRoomOrPlayer(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)

	assertWebSocketDialError(t, server.URL, "/rooms/missing/players/player_missing", http.StatusNotFound, "room_not_found")
	assertWebSocketDialError(t, server.URL, "/rooms/"+room.ID+"/players/player_missing", http.StatusNotFound, "player_not_found")
}

func TestWebSocketRejectsDuplicateSamePlayerConnection(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)

	first := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer first.Close(websocket.StatusNormalClosure, "")

	assertWebSocketDialError(t, server.URL, player.WebSocketPath, http.StatusConflict, "player_already_connected")
}

func TestWebSocketRouteAcceptsPercentEncodedIDs(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	encodedPath := strings.Replace(player.WebSocketPath, "room_", "room%5F", 1)
	encodedPath = strings.Replace(encodedPath, "player_", "player%5F", 1)

	conn := dialIssuedPlayer(t, server.URL, encodedPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)
}

func TestWebSocketAllowsWaitingRoomConnectionWithoutBroadcasting(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
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

func TestWebSocketMatchmakingSendsReadyEventWithMapAndSpawnPositions(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	red := joinMatchmaking(t, handler)
	blue := joinMatchmaking(t, handler)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, red.Room.ID, red.Player.ID)
	waitForAttachedClient(t, store, blue.Room.ID, blue.Player.ID)

	redReady := readReadyEventMessage(t, redConn)
	blueReady := readReadyEventMessage(t, blueConn)
	assertMatchingReadyEvents(t, redReady, blueReady)

	if redReady.Type != "Ready" {
		t.Fatalf("expected Ready event type, got %q", redReady.Type)
	}
	if redReady.Map.Width != red.Room.Map.Width || redReady.Map.Height != red.Room.Map.Height {
		t.Fatalf("expected ready map size %dx%d, got %dx%d", red.Room.Map.Width, red.Room.Map.Height, redReady.Map.Width, redReady.Map.Height)
	}
	if len(redReady.Map.Map) != red.Room.Map.Height || len(redReady.Map.Map[0]) != red.Room.Map.Width {
		t.Fatalf("expected ready map tile grid %dx%d, got %+v", red.Room.Map.Width, red.Room.Map.Height, redReady.Map.Map)
	}
	if redReady.Map.Map[0][0] != int(simulation.TileWall) {
		t.Fatalf("expected ready map rows to be number arrays, got %+v", redReady.Map.Map[0])
	}
	if len(redReady.Players) != 2 {
		t.Fatalf("expected two ready players, got %+v", redReady.Players)
	}
	assertReadyPlayerTeamSlot(t, redReady.Players, red.Player.ID, "red", 0)
	assertReadyPlayerTeamSlot(t, redReady.Players, blue.Player.ID, "blue", 0)

	assignments := simulation.PlayerAssignments([]simulation.PlayerID{
		simulation.PlayerID(red.Player.ID),
		simulation.PlayerID(blue.Player.ID),
	}, store.gameConfig)
	assertReadyPlayerSpawn(t, redReady.Players, red.Player.ID, assignments[0].SpawnPosition)
	assertReadyPlayerSpawn(t, redReady.Players, blue.Player.ID, assignments[1].SpawnPosition)
}

func TestWebSocketMatchmakingUsesSnapshotStatusForReadyCountdownAndStart(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	red := joinMatchmaking(t, handler)
	blue := joinMatchmaking(t, handler)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, red.Room.ID, red.Player.ID)
	waitForAttachedClient(t, store, blue.Room.ID, blue.Player.ID)

	redReady := readReadyEventMessage(t, redConn)
	blueReady := readReadyEventMessage(t, blueConn)
	assertMatchingReadyEvents(t, redReady, blueReady)

	detailRec := request(handler, http.MethodGet, "/rooms/"+red.Room.ID)
	var detail roomResponse
	decodeResponse(t, detailRec, &detail)
	if detail.LatestSnapshot.Tick != 0 {
		t.Fatalf("expected no gameplay snapshot before ready, got latest tick %d", detail.LatestSnapshot.Tick)
	}

	writeWSJSON(t, redConn, readyMessage{Type: "ready"})
	writeWSJSON(t, blueConn, readyMessage{Type: "ready"})
	redStarting := readUntilSnapshotStatus(t, redConn, "starting")
	blueStarting := readUntilSnapshotStatus(t, blueConn, "starting")
	assertMatchingMatchSnapshots(t, redStarting, blueStarting)
	if redStarting.Snapshot.Countdown != 5 {
		t.Fatalf("expected starting countdown 5, got %+v", redStarting.Snapshot)
	}

	for i := 0; i < 4; i++ {
		fakeClock.TickTicker(time.Second, 0)
	}

	fakeClock.TickTicker(time.Second, 0)
	redStarted := readUntilSnapshotStatus(t, redConn, "started")
	blueStarted := readUntilSnapshotStatus(t, blueConn, "started")
	assertMatchingMatchSnapshots(t, redStarted, blueStarted)

	fakeClock.TickTicker(gameplayInterval, 0)
	gameplay := readSnapshotMessage(t, redConn)
	if gameplay.Snapshot.Tick != 1 {
		t.Fatalf("expected first gameplay snapshot tick 1 after countdown, got %d", gameplay.Snapshot.Tick)
	}
}

func TestWebSocketCloseBeforeMatchStartCancelsMatchedRoom(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	red := joinMatchmaking(t, handler)
	blue := joinMatchmaking(t, handler)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, red.Room.ID, red.Player.ID)
	waitForAttachedClient(t, store, blue.Room.ID, blue.Player.ID)
	_ = readReadyEventMessage(t, redConn)
	_ = readReadyEventMessage(t, blueConn)

	_ = redConn.Close(websocket.StatusNormalClosure, "")
	waitForRoomDeleted(t, store, red.Room.ID)

	rec := request(handler, http.MethodGet, "/rooms/"+red.Room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected pre-start close to cancel matched room, got status %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func TestWebSocketKeepsSnapshotStreamAfterInvalidInput(t *testing.T) {
	fakeClock := newFakeClock()
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

	writeText(t, conn, "{not-json")
	invalidInput := readErrorMessage(t, conn)
	if invalidInput.Error.Code != "invalid_input" {
		t.Fatalf("expected invalid_input error, got %+v", invalidInput.Error)
	}

	fakeClock.TickTicker(gameplayInterval, 0)
	message := readSnapshotMessage(t, conn)
	if message.Snapshot.Tick != 1 {
		t.Fatalf("expected stream to continue with tick 1, got %d", message.Snapshot.Tick)
	}
}

func TestWebSocketSendsErrorMessageAfterInvalidInputAndKeepsSnapshotStream(t *testing.T) {
	fakeClock := newFakeClock()
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

	writeText(t, conn, "{not-json")
	errorMessage := readErrorMessage(t, conn)
	if errorMessage.Type != "error" {
		t.Fatalf("expected error message type, got %q", errorMessage.Type)
	}
	if errorMessage.Error.Code != "invalid_input" {
		t.Fatalf("expected invalid_input error, got %+v", errorMessage.Error)
	}

	fakeClock.TickTicker(gameplayInterval, 0)
	snapshot := readSnapshotMessage(t, conn)
	if snapshot.Snapshot.Tick != 1 {
		t.Fatalf("expected stream to continue with tick 1, got %d", snapshot.Snapshot.Tick)
	}
}

func TestStoreCleansUpStartedRoomAfterAllPlayersDisconnect(t *testing.T) {
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
	waitForAttachedClient(t, store, room.ID, player.ID)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	waitForDetachedClient(t, store, room.ID, player.ID)

	fakeClock.Advance(5*time.Minute - time.Nanosecond)
	if rec := request(handler, http.MethodGet, "/rooms/"+room.ID); rec.Code != http.StatusOK {
		t.Fatalf("expected disconnected started room before TTL to exist, got status %d", rec.Code)
	}

	fakeClock.Advance(time.Nanosecond)
	fakeClock.TickTicker(janitorInterval, 0)
	waitForRoomDeleted(t, store, room.ID)
	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected all-disconnected started room after TTL to be cleaned up, got status %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func TestStoreKeepsConnectedRoomPastDisconnectedCleanupTTL(t *testing.T) {
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
	sentinel, err := store.createRoom()
	if err != nil {
		t.Fatalf("create expired sweep sentinel: %v", err)
	}
	sentinelRoom := store.lookupRoom(sentinel.ID)
	sentinelRoom.mu.Lock()
	sentinelRoom.lastActivityAt = fakeClock.Now().Add(-defaultWaitingRoomIdleTTL)
	sentinelRoom.mu.Unlock()

	fakeClock.Advance(5 * time.Minute)
	fakeClock.TickTicker(janitorInterval, 0)
	waitForRoomDeleted(t, store, sentinel.ID)
	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected connected room to survive cleanup TTL, got status %d", rec.Code)
	}
}

func TestWaitingRoomAcceptsInputButDoesNotBroadcastSnapshot(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
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

	writeWSJSON(t, conn, inputMessage{MoveDir: simulation.Vector2{X: 1, Y: 0}})
	waitForPendingInput(t, store, room.ID, player.ID)
	fakeClock.TickTicker(gameplayInterval, 0)
	message := readSnapshotMessage(t, conn)
	if message.Snapshot.Players[0].Pos.X == 0 {
		t.Fatalf("expected valid input to move player, got %+v", message.Snapshot.Players[0].Pos)
	}
}

func TestWebSocketUsesClientCompatibleMessageFieldNames(t *testing.T) {
	fakeClock := newFakeClock()
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

	writeText(t, conn, `{"MoveDir":{"x":1,"y":0},"AttackDir":{"x":0,"y":1},"PressedAttack":true}`)
	waitForPendingInput(t, store, room.ID, player.ID)
	fakeClock.TickTicker(gameplayInterval, 0)

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
	store := newStore(5, fakeClock, StoreConfig{GameConfig: fastRechargeGameConfig()})
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	red := issuePlayer(t, handler, room.ID)
	blue := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
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

func TestWebSocketSendsGameEndWinLoseAndCleansUpRoom(t *testing.T) {
	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{
		Map:        verticalDuelMap(),
		GameConfig: fastRechargeGameConfig(),
	})
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	red := issuePlayer(t, handler, room.ID)
	blue := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, red.ID)
	waitForAttachedClient(t, store, room.ID, blue.ID)

	for hitCount := 0; hitCount < 10; hitCount++ {
		writeWSJSON(t, redConn, inputMessage{
			AttackDir:     simulation.Vector2{X: 0, Y: -1},
			PressedAttack: true,
		})
		waitForPendingInput(t, store, room.ID, red.ID)
		tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
		tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
	}

	assertGameEnd(t, readGameEndMessage(t, redConn), red.ID, "Win")
	assertGameEnd(t, readGameEndMessage(t, blueConn), blue.ID, "Lose")
	waitForRoomDeleted(t, store, room.ID)
}

func TestWebSocketSendsDrawToBothPlayersWhenBothDieOnSameTick(t *testing.T) {
	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{
		Map:        verticalDuelMap(),
		GameConfig: fastRechargeGameConfig(),
	})
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	red := issuePlayer(t, handler, room.ID)
	blue := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, red.ID)
	waitForAttachedClient(t, store, room.ID, blue.ID)

	for hitCount := 0; hitCount < 10; hitCount++ {
		writeWSJSON(t, redConn, inputMessage{
			AttackDir:     simulation.Vector2{X: 0, Y: -1},
			PressedAttack: true,
		})
		writeWSJSON(t, blueConn, inputMessage{
			AttackDir:     simulation.Vector2{X: 0, Y: 1},
			PressedAttack: true,
		})
		waitForPendingInput(t, store, room.ID, red.ID)
		waitForPendingInput(t, store, room.ID, blue.ID)
		tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
		tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
	}

	assertGameEnd(t, readGameEndMessage(t, redConn), red.ID, "Draw")
	assertGameEnd(t, readGameEndMessage(t, blueConn), blue.ID, "Draw")
	waitForRoomDeleted(t, store, room.ID)
}

func TestStoreTicksRoomsInParallel(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)

	first := createStartedRoomInStore(t, store)
	second := createStartedRoomInStore(t, store)
	firstRoom := store.lookupRoom(first.ID)
	secondRoom := store.lookupRoom(second.ID)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstRoom.mu.Lock()
	firstStepper := firstRoom.state
	firstRoom.state = testRoomStepper(func(inputs []simulation.InputCommand) simulation.Snapshot {
		close(firstStarted)
		<-releaseFirst
		return firstStepper.Step(inputs)
	})
	firstRoom.mu.Unlock()

	firstDone := make(chan struct{})
	go func() {
		store.tickRoom(first.ID)
		close(firstDone)
	}()
	<-firstStarted

	secondDone := make(chan struct{})
	go func() {
		store.tickRoom(second.ID)
		close(secondDone)
	}()

	secondCompletedWhileFirstBlocked := false
	select {
	case <-secondDone:
		secondCompletedWhileFirstBlocked = true
	case <-time.After(250 * time.Millisecond):
	}
	close(releaseFirst)
	<-firstDone
	if !secondCompletedWhileFirstBlocked {
		t.Fatal("expected room B tick to complete while room A step is blocked")
	}

	secondRoom.mu.Lock()
	secondTick := secondRoom.latestSnapshot.Tick
	secondRoom.mu.Unlock()
	if secondTick != 1 {
		t.Fatalf("expected room B to advance to tick 1, got %d", secondTick)
	}
}

func TestStoreStaleRoomTickPreservesReplacementPlayerID(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	started := createStartedRoomInStore(t, store)
	original := store.lookupRoom(started.ID)
	player := started.Players[0]

	original.mu.Lock()
	original.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
		return simulation.Snapshot{Players: []simulation.PlayerData{{
			ID:     simulation.PlayerID(player.ID),
			IsDead: true,
		}}}
	})
	original.mu.Unlock()

	store.mu.Lock()
	replacement := store.newRoomLocked(started.ID, store.gameConfig)
	replacement.Players = append(replacement.Players, player)
	store.rooms[started.ID] = replacement
	store.mu.Unlock()

	store.tickRoomState(original)
	if got := store.lookupRoom(started.ID); got != replacement {
		t.Fatal("expected stale room tick not to delete replacement")
	}
	store.mu.RLock()
	_, playerIDReserved := store.playerIDs[player.ID]
	store.mu.RUnlock()
	if !playerIDReserved {
		t.Fatal("expected stale room cleanup not to release replacement player ID")
	}
}

func TestStoreConcurrentInputListDeleteAndTick(t *testing.T) {
	store := NewStoreWithClock(64, newFakeClock())
	t.Cleanup(store.Close)

	for range 32 {
		started := createStartedRoomInStore(t, store)
		playerID := started.Players[0].ID
		session := newClientSession(newFakeClientConn(false), nil)
		room := store.lookupRoom(started.ID)
		room.mu.Lock()
		room.clients[playerID] = session
		room.mu.Unlock()
		begin := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(4)
		go func() {
			defer wg.Done()
			<-begin
			store.setInput(started.ID, playerID, inputMessage{MoveDir: simulation.Vector2{X: 1}}, session)
		}()
		go func() {
			defer wg.Done()
			<-begin
			_ = store.listRooms()
		}()
		go func() {
			defer wg.Done()
			<-begin
			store.tickRoom(started.ID)
		}()
		go func() {
			defer wg.Done()
			<-begin
			store.deleteRoom(started.ID)
		}()
		close(begin)
		wg.Wait()

		if got := store.lookupRoom(started.ID); got != nil {
			t.Fatalf("expected concurrently deleted room %q to leave the registry", started.ID)
		}
	}
}

func TestStoreConcurrentReservationAndDeleteRejectsStaleAttach(t *testing.T) {
	store := NewStoreWithClock(64, newFakeClock())
	t.Cleanup(store.Close)
	resourceTickers := make([]*countingTicker, 0, 32)

	for range 32 {
		created, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		issued, err := store.addPlayer(created.ID)
		if err != nil {
			t.Fatalf("add player: %v", err)
		}
		resourceTicker := newCountingTicker()
		resourceTickers = append(resourceTickers, resourceTicker)
		resourceStop := make(chan struct{})
		room := store.lookupRoom(created.ID)
		room.mu.Lock()
		room.ticker = resourceTicker
		room.stop = resourceStop
		room.mu.Unlock()

		begin := make(chan struct{})
		var wg sync.WaitGroup
		var reservation *clientReservation
		var reserveErr error
		var deleted bool
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-begin
			reservation, reserveErr = store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
		}()
		go func() {
			defer wg.Done()
			<-begin
			_, deleted = store.deleteRoom(created.ID)
		}()
		close(begin)
		wg.Wait()

		if reserveErr != nil && !errors.Is(reserveErr, ErrRoomNotFound) {
			t.Fatalf("expected reservation or room-not-found, got %v", reserveErr)
		}
		if !deleted {
			t.Fatal("expected concurrent delete to remove the room")
		}
		if reservation != nil && store.attachClient(reservation, nil) {
			t.Fatal("expected reservation from a deleted room not to attach")
		}
		if got := store.lookupRoom(created.ID); got != nil {
			t.Fatalf("expected deleted room %q to leave the registry", created.ID)
		}
		select {
		case <-resourceStop:
		default:
			t.Fatal("expected deleted room stop channel to close")
		}
	}
	for index, resourceTicker := range resourceTickers {
		if got := resourceTicker.stopCount.Load(); got != 1 {
			t.Fatalf("expected room %d ticker to stop exactly once, got %d", index, got)
		}
	}
}

func TestStoreConcurrentCountdownAndDelete(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add player: %v", err)
	}

	countdownTicker := fakeClock.NewTicker(time.Second)
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.matchStatus = MatchStatusStarting
	room.countdown = 2
	room.countdownTicker = countdownTicker
	room.countdownStop = make(chan struct{})
	room.mu.Unlock()

	begin := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-begin
		store.tickMatchCountdown(created.ID, countdownTicker)
	}()
	go func() {
		defer wg.Done()
		<-begin
		store.deleteRoom(created.ID)
	}()
	close(begin)
	wg.Wait()

	if got := store.lookupRoom(created.ID); got != nil {
		t.Fatal("expected countdown/delete race to remove the room")
	}
}

func TestStoreCountdownNaturalCompletionStopsTickerOnce(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
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
		t.Fatal("expected final countdown tick to complete")
	}
	if got := countdownTicker.stopCount.Load(); got != 1 {
		t.Fatalf("expected countdown ticker to stop exactly once, got %d", got)
	}
	room.mu.Lock()
	status := room.matchStatus
	room.mu.Unlock()
	if status != MatchStatusStarted {
		t.Fatalf("expected room to start after countdown, got %q", status)
	}
}

func TestFakeClockTicksIndependentTickersWithSameDuration(t *testing.T) {
	fakeClock := newFakeClock()
	first := fakeClock.NewTicker(time.Second)
	second := fakeClock.NewTicker(time.Second)

	fakeClock.TickTicker(time.Second, 0)
	select {
	case <-first.C():
	default:
		t.Fatal("expected first ticker to receive its tick")
	}
	select {
	case <-second.C():
		t.Fatal("expected second ticker not to receive the first ticker's tick")
	default:
	}
}

type testRoomStepper func([]simulation.InputCommand) simulation.Snapshot

func (step testRoomStepper) Step(inputs []simulation.InputCommand) simulation.Snapshot {
	return step(inputs)
}

func createStartedRoomInStore(t *testing.T, store *Store) roomResponse {
	t.Helper()

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add player: %v", err)
	}
	started, err := store.startRoom(created.ID)
	if err != nil {
		t.Fatalf("start room: %v", err)
	}
	return started
}

type countingTicker struct {
	ticks     chan time.Time
	stopCount atomic.Int32
}

func newCountingTicker() *countingTicker {
	return &countingTicker{ticks: make(chan time.Time)}
}

func (t *countingTicker) C() <-chan time.Time {
	return t.ticks
}

func (t *countingTicker) Stop() {
	t.stopCount.Add(1)
}

type blockingNextNowClock struct {
	base       *fakeClock
	blockNext  atomic.Bool
	nowStarted chan struct{}
	allowNow   chan struct{}
}

func (c *blockingNextNowClock) Now() time.Time {
	if c.blockNext.CompareAndSwap(true, false) {
		close(c.nowStarted)
		<-c.allowNow
	}
	return c.base.Now()
}

func (c *blockingNextNowClock) NewTicker(duration time.Duration) ticker {
	return c.base.NewTicker(duration)
}

type fakeClock struct {
	mu        sync.Mutex
	tickers   []*fakeTicker
	stopCount int
	now       time.Time
}

type fakeTicker struct {
	clock    *fakeClock
	duration time.Duration
	ticks    chan time.Time
	stopped  bool
}

func newFakeClock() *fakeClock {
	return newFakeClockAt(time.Date(2026, 5, 30, 7, 0, 0, 0, time.UTC))
}

func fastRechargeGameConfig() simulation.GameConfig {
	config := singleModeGameConfig(simulation.DefaultGameModeConfig())
	config.Player.Types[0].AttackRechargeTicks = 1
	return config
}

func newFakeClockAt(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTicker(duration time.Duration) ticker {
	c.mu.Lock()
	defer c.mu.Unlock()

	ticker := &fakeTicker{
		clock:    c,
		duration: duration,
		ticks:    make(chan time.Time, 8),
	}
	c.tickers = append(c.tickers, ticker)
	return ticker
}

func (t *fakeTicker) C() <-chan time.Time {
	return t.ticks
}

func (t *fakeTicker) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return
	}
	t.stopped = true
	t.clock.stopCount++
}

func (c *fakeClock) Tick() {
	c.mu.Lock()
	var ticker *fakeTicker
	for index := len(c.tickers) - 1; index >= 0; index-- {
		if !c.tickers[index].stopped {
			ticker = c.tickers[index]
			break
		}
	}
	c.mu.Unlock()
	if ticker != nil {
		ticker.tick()
	}
}

func (c *fakeClock) TickTicker(duration time.Duration, ordinal int) {
	c.mu.Lock()
	var ticker *fakeTicker
	for _, candidate := range c.tickers {
		if candidate.duration != duration {
			continue
		}
		if ordinal == 0 {
			ticker = candidate
			break
		}
		ordinal--
	}
	c.mu.Unlock()
	if ticker != nil {
		ticker.tick()
	}
}

func (t *fakeTicker) tick() {
	t.clock.mu.Lock()
	if t.stopped {
		t.clock.mu.Unlock()
		return
	}
	now := t.clock.now
	t.clock.mu.Unlock()
	t.ticks <- now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func (c *fakeClock) RequestedDuration() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.tickers) == 0 {
		return 0
	}
	return c.tickers[len(c.tickers)-1].duration

}

func (c *fakeClock) TickerCount(duration time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	for _, ticker := range c.tickers {
		if ticker.duration == duration {
			count++
		}
	}
	return count
}

func (c *fakeClock) TotalTickerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tickers)
}

func (c *fakeClock) StopCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopCount
}

func waitForFakeTickerCount(clock *fakeClock, duration time.Duration, count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if clock.TickerCount(duration) >= count {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return clock.TickerCount(duration) >= count
}

func readFakeMatchSnapshot(t *testing.T, conn *fakeClientConn) matchSnapshotMessage {
	t.Helper()
	select {
	case payload := <-conn.writes:
		var message matchSnapshotMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatalf("decode fake match snapshot: %v", err)
		}
		return message
	case <-time.After(time.Second):
		t.Fatal("expected fake match snapshot")
		return matchSnapshotMessage{}
	}
}

func waitForPendingInput(t *testing.T, store *Store, roomID string, playerID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		room := store.lookupRoom(roomID)
		ok := false
		if room != nil {
			room.mu.Lock()
			_, ok = room.pendingInputs[playerID]
			room.mu.Unlock()
		}
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
		room := store.lookupRoom(roomID)
		var session *clientSession
		if room != nil {
			room.mu.Lock()
			session = room.clients[playerID]
			room.mu.Unlock()
		}
		if session != nil {
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
		room := store.lookupRoom(roomID)
		ok := false
		if room != nil {
			room.mu.Lock()
			_, ok = room.clients[playerID]
			room.mu.Unlock()
		}
		if !ok {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("expected detached websocket client for player %s", playerID)
}

func waitForRoomDeleted(t *testing.T, store *Store, roomID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.lookupRoom(roomID) == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("expected room %s to be deleted", roomID)
}

func tickAndReadMatchingSnapshots(t *testing.T, fakeClock *fakeClock, first *websocket.Conn, second *websocket.Conn) snapshotMessage {
	t.Helper()

	fakeClock.TickTicker(gameplayInterval, 0)
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

func assertMatchingMatchSnapshots(t *testing.T, first matchSnapshotMessage, second matchSnapshotMessage) {
	t.Helper()

	firstPayload, err := json.Marshal(first.Snapshot)
	if err != nil {
		t.Fatalf("marshal first match snapshot: %v", err)
	}
	secondPayload, err := json.Marshal(second.Snapshot)
	if err != nil {
		t.Fatalf("marshal second match snapshot: %v", err)
	}
	if string(firstPayload) != string(secondPayload) {
		t.Fatalf("expected matching match snapshots, got first %s and second %s", firstPayload, secondPayload)
	}
}

func assertMatchingReadyEvents(t *testing.T, first readyEventMessage, second readyEventMessage) {
	t.Helper()

	firstPayload, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first ready event: %v", err)
	}
	secondPayload, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second ready event: %v", err)
	}
	if string(firstPayload) != string(secondPayload) {
		t.Fatalf("expected matching ready events, got first %s and second %s", firstPayload, secondPayload)
	}
}

func assertReadyPlayerSpawn(t *testing.T, players []readyEventPlayer, playerID string, position simulation.Vector2) {
	t.Helper()

	for _, player := range players {
		if player.ID != playerID {
			continue
		}
		if player.SpawnPosition != position {
			t.Fatalf("expected player %s spawn %+v, got %+v", playerID, position, player.SpawnPosition)
		}
		return
	}

	t.Fatalf("expected ready event to include player %s in %+v", playerID, players)
}

func assertReadyPlayerTeamSlot(t *testing.T, players []readyEventPlayer, playerID string, team string, slot int) {
	t.Helper()

	for _, player := range players {
		if player.ID != playerID {
			continue
		}
		if player.Team != team || player.Slot != slot {
			t.Fatalf("expected player %s to be %s slot %d, got %+v", playerID, team, slot, player)
		}
		return
	}

	t.Fatalf("expected ready event to include player %s in %+v", playerID, players)
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
	return issuePlayer(t, handler, roomID).playerResponse
}

func issuePlayer(t *testing.T, handler http.Handler, roomID string) issuedPlayer {
	t.Helper()

	rec := request(handler, http.MethodPost, "/rooms/"+roomID+"/players")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected create player status 201, got %d", rec.Code)
	}
	var issued playerSessionResponse
	decodeResponse(t, rec, &issued)
	return issuedPlayer{
		playerResponse: issued.Player,
		SessionToken:   issued.SessionToken,
		WebSocketPath:  issued.WebSocketPath,
	}
}

func dialIssuedPlayer(t *testing.T, serverURL string, webSocketPath string) *websocket.Conn {
	t.Helper()

	conn, _, err := websocket.Dial(context.Background(), websocketURLForPath(serverURL, webSocketPath), nil)
	if err != nil {
		t.Fatal("dial issued websocket connection failed")
	}
	return conn
}

func assertWebSocketDialError(t *testing.T, serverURL string, webSocketPath string, status int, code string) {
	t.Helper()

	conn, resp, err := websocket.Dial(context.Background(), websocketURLForPath(serverURL, webSocketPath), nil)
	if err == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatalf("expected websocket dial to fail with status %d", status)
	}
	assertWebSocketErrorResponse(t, resp, status, code)
}

func websocketURLForPath(serverURL string, webSocketPath string) string {
	return "ws" + serverURL[len("http"):] + webSocketPath
}

func startRoom(t *testing.T, handler http.Handler, roomID string) {
	t.Helper()

	rec := request(handler, http.MethodPost, "/rooms/"+roomID+"/start")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected start room status 200, got %d", rec.Code)
	}
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

func readReadyEventMessage(t *testing.T, conn *websocket.Conn) readyEventMessage {
	t.Helper()

	payload := readWebSocketPayload(t, conn)

	var message readyEventMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode ready event message: %v", err)
	}
	return message
}

func readGameEndMessage(t *testing.T, conn *websocket.Conn) gameEndMessage {
	t.Helper()

	payload := readWebSocketPayload(t, conn)

	var message gameEndMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode game end message: %v", err)
	}
	return message
}

func readUntilSnapshotStatus(t *testing.T, conn *websocket.Conn, status string) matchSnapshotMessage {
	t.Helper()

	for i := 0; i < 4; i++ {
		payload := readWebSocketPayload(t, conn)
		var message matchSnapshotMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatalf("decode match snapshot message: %v", err)
		}
		if message.Type != "snapshot" {
			t.Fatalf("expected snapshot message while waiting for status %q, got %q", status, message.Type)
		}
		if message.Snapshot.Status == status {
			return message
		}
	}

	t.Fatalf("expected snapshot status %q", status)
	return matchSnapshotMessage{}
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

type readyMessage struct {
	Type string `json:"Type"`
}

type matchSnapshotMessage struct {
	Type     string `json:"Type"`
	Snapshot struct {
		Status      string                      `json:"status"`
		Countdown   int                         `json:"countdown,omitempty"`
		Tick        simulation.Tick             `json:"Tick"`
		Players     []simulation.PlayerData     `json:"Players"`
		Projectiles []simulation.ProjectileData `json:"Projectiles"`
	} `json:"Snapshot"`
}

func assertGameEnd(t *testing.T, message gameEndMessage, playerID string, result string) {
	t.Helper()

	if message.Type != "GameEnd" {
		t.Fatalf("expected GameEnd message type, got %+v", message)
	}
	if message.PlayerID != playerID {
		t.Fatalf("expected GameEnd player %s, got %+v", playerID, message)
	}
	if message.Result != result {
		t.Fatalf("expected GameEnd result %s, got %+v", result, message)
	}
}

func verticalDuelMap() simulation.MapData {
	return simulation.MapData{
		Width:      5,
		Height:     5,
		Index:      0,
		MaxPlayers: 2,
		TileSize:   simulation.TileSize,
		Map: [][]simulation.TileType{
			{simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall},
			{simulation.TileWall, simulation.TileGround, simulation.TileSpawnPoint, simulation.TileGround, simulation.TileWall},
			{simulation.TileWall, simulation.TileGround, simulation.TileSpawnPoint, simulation.TileGround, simulation.TileWall},
			{simulation.TileWall, simulation.TileGround, simulation.TileGround, simulation.TileGround, simulation.TileWall},
			{simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall},
		},
	}
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
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected websocket application/json content type, got %q", got)
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
	wantMessage := map[string]string{
		"player_already_connected": "player already connected",
		"player_not_found":         "player not found",
		"room_not_found":           "room not found",
		"unauthorized":             "unauthorized",
	}[code]
	if body.Error.Message != wantMessage {
		t.Fatalf("expected websocket error message %q, got %+v", wantMessage, body.Error)
	}
}
