package rooms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

func TestStoreShutdownBlocksNewMutationAndDrainsInFlightMutation(t *testing.T) {
	t.Run("drains mutation that already entered", func(t *testing.T) {
		clock := newBlockingStopClock()
		random := newShutdownBarrierReader(0x41)
		store := newStore(5, clock, StoreConfig{Random: random})
		t.Cleanup(func() {
			random.release()
			clock.ticker.release()
			store.Close()
		})

		mutationResult := make(chan struct {
			room roomResponse
			err  error
		}, 1)
		go func() {
			created, err := store.createRoom()
			mutationResult <- struct {
				room roomResponse
				err  error
			}{room: created, err: err}
		}()
		waitShutdownSignal(t, random.entered, "in-flight mutation random read")

		shutdownResult := startStoreShutdown(store, context.Background())
		random.release()

		var created roomResponse
		select {
		case result := <-mutationResult:
			if result.err != nil {
				t.Fatalf("expected in-flight mutation to commit before shutdown: %v", result.err)
			}
			created = result.room
		case <-time.After(time.Second):
			t.Fatal("expected in-flight mutation to drain")
		}
		waitShutdownSignal(t, clock.ticker.stopStarted, "shutdown janitor stop")
		if got := store.lookupRoom(created.ID); got == nil {
			t.Fatal("expected shutdown to drain the in-flight mutation before teardown")
		}

		clock.ticker.release()
		if err := waitStoreShutdown(t, shutdownResult); err != nil {
			t.Fatalf("shutdown after draining mutation: %v", err)
		}
	})

	type externalMutation struct {
		name           string
		assertRejected func(*testing.T, *Store, *room, playerSessionResponse, playerSessionResponse, *clientReservation, *clientSession)
	}
	mutations := []externalMutation{
		{
			name: "create room",
			assertRejected: func(t *testing.T, store *Store, _ *room, _, _ playerSessionResponse, _ *clientReservation, _ *clientSession) {
				_, err := store.createRoom()
				if !errors.Is(err, ErrInternal) {
					t.Fatalf("expected create ErrInternal, got %v", err)
				}
			},
		},
		{
			name: "clear rooms",
			assertRejected: func(t *testing.T, store *Store, _ *room, _, _ playerSessionResponse, _ *clientReservation, _ *clientSession) {
				if cleared := store.clearRooms(); cleared.Deleted != 0 {
					t.Fatalf("expected clear Deleted=0, got %+v", cleared)
				}
			},
		},
		{
			name: "delete room",
			assertRejected: func(t *testing.T, store *Store, room *room, _, _ playerSessionResponse, _ *clientReservation, _ *clientSession) {
				response, deleted := store.deleteRoom(room.ID)
				if deleted || response.Deleted != 0 {
					t.Fatalf("expected delete false/Deleted=0, got deleted=%t response=%+v", deleted, response)
				}
			},
		},
		{
			name: "add player",
			assertRejected: func(t *testing.T, store *Store, room *room, _, _ playerSessionResponse, _ *clientReservation, _ *clientSession) {
				_, err := store.addPlayer(room.ID)
				if !errors.Is(err, ErrInternal) {
					t.Fatalf("expected add player ErrInternal, got %v", err)
				}
			},
		},
		{
			name: "join matchmaking",
			assertRejected: func(t *testing.T, store *Store, _ *room, _, _ playerSessionResponse, _ *clientReservation, _ *clientSession) {
				_, err := store.joinMatchmaking(store.defaultGameMode())
				if !errors.Is(err, ErrInternal) {
					t.Fatalf("expected join matchmaking ErrInternal, got %v", err)
				}
			},
		},
		{
			name: "start room",
			assertRejected: func(t *testing.T, store *Store, room *room, _, _ playerSessionResponse, _ *clientReservation, _ *clientSession) {
				_, err := store.startRoom(room.ID)
				if !errors.Is(err, ErrInternal) {
					t.Fatalf("expected start room ErrInternal, got %v", err)
				}
			},
		},
		{
			name: "reserve websocket client",
			assertRejected: func(t *testing.T, store *Store, room *room, available, _ playerSessionResponse, _ *clientReservation, _ *clientSession) {
				_, err := store.reserveClient(room.ID, available.Player.ID, []string{available.SessionToken})
				if !errors.Is(err, ErrRoomNotFound) {
					t.Fatalf("expected reserve ErrRoomNotFound, got %v", err)
				}
			},
		},
		{
			name: "attach websocket client",
			assertRejected: func(t *testing.T, store *Store, _ *room, _, _ playerSessionResponse, reservation *clientReservation, _ *clientSession) {
				_, attached := store.attachClientSession(reservation, newFakeClientConn(false))
				if attached {
					t.Fatal("expected attach false")
				}
			},
		},
		{
			name: "set input",
			assertRejected: func(_ *testing.T, store *Store, room *room, _, current playerSessionResponse, _ *clientReservation, session *clientSession) {
				store.setInput(room.ID, current.Player.ID, inputMessage{PressedAttack: true}, session)
			},
		},
		{
			name: "mark client ready",
			assertRejected: func(_ *testing.T, store *Store, room *room, _, current playerSessionResponse, _ *clientReservation, session *clientSession) {
				store.markClientReady(room.ID, current.Player.ID, session)
			},
		},
	}

	for _, mutation := range mutations {
		t.Run("rejects "+mutation.name+" after quiescing", func(t *testing.T) {
			clock := newBlockingStopClock()
			store := NewStoreWithClock(5, clock)
			t.Cleanup(func() {
				clock.ticker.release()
				store.Close()
			})

			created, err := store.createRoom()
			if err != nil {
				t.Fatalf("create room: %v", err)
			}
			reservedPlayer, err := store.addPlayer(created.ID)
			if err != nil {
				t.Fatalf("add reserved player: %v", err)
			}
			availablePlayer, err := store.addPlayer(created.ID)
			if err != nil {
				t.Fatalf("add available player: %v", err)
			}
			currentPlayer, err := store.addPlayer(created.ID)
			if err != nil {
				t.Fatalf("add current player: %v", err)
			}
			reservation, err := store.reserveClient(created.ID, reservedPlayer.Player.ID, []string{reservedPlayer.SessionToken})
			if err != nil {
				t.Fatalf("reserve client before shutdown: %v", err)
			}
			currentSession := attachHeartbeatTestSession(
				t,
				store,
				created.ID,
				currentPlayer.Player.ID,
				currentPlayer.SessionToken,
				newFakeClientConn(false),
			)
			capturedRoom := store.lookupRoom(created.ID)
			capturedRoom.mu.Lock()
			capturedRoom.matchStatus = MatchStatusLoading
			capturedRoom.readyPlayers = make(map[string]bool)
			capturedRoom.mu.Unlock()
			before := captureShutdownMutationState(store, capturedRoom)

			shutdownResult := startStoreShutdown(store, context.Background())
			waitShutdownSignal(t, clock.ticker.stopStarted, "shutdown quiescing barrier")
			mutation.assertRejected(t, store, capturedRoom, availablePlayer, currentPlayer, reservation, currentSession)
			after := captureShutdownMutationState(store, capturedRoom)
			if after != before {
				t.Fatalf("%s mutated shutdown state: before=%+v after=%+v", mutation.name, before, after)
			}

			clock.ticker.release()
			if err := waitStoreShutdown(t, shutdownResult); err != nil {
				t.Fatalf("shutdown after %s: %v", mutation.name, err)
			}
		})
	}
}

func TestStoreShutdownWaitsForSharedMutationGate(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	room := store.lookupRoom(created.ID)
	if room == nil {
		t.Fatal("expected created room")
	}

	random := newShutdownBarrierReader(0x42)
	store.mu.Lock()
	store.random = random
	store.mu.Unlock()
	room.mu.Lock()
	var releaseRoomOnce sync.Once
	releaseRoom := func() {
		releaseRoomOnce.Do(func() { room.mu.Unlock() })
	}
	t.Cleanup(func() {
		random.release()
		releaseRoom()
		store.Close()
	})

	addResult := make(chan error, 1)
	go func() {
		_, addErr := store.addPlayer(created.ID)
		addResult <- addErr
	}()
	waitShutdownSignal(t, random.entered, "addPlayer credential random read")
	random.release()
	waitShutdownCondition(t, "addPlayer credential phase completion", func() bool {
		if !store.mu.TryLock() {
			return false
		}
		store.mu.Unlock()
		return true
	})
	if store.mutationMu.TryLock() {
		store.mutationMu.Unlock()
		t.Fatal("expected addPlayer to retain the shared mutation read gate while room commit was blocked")
	}

	shutdownResult := startStoreShutdown(store, context.Background())
	waitShutdownCondition(t, "Shutdown mutation writer pending", func() bool {
		if !store.mutationMu.TryRLock() {
			return true
		}
		store.mutationMu.RUnlock()
		return false
	})
	store.mu.RLock()
	closedWhileMutationBlocked := store.closed
	store.mu.RUnlock()
	if closedWhileMutationBlocked {
		t.Fatal("expected Store to remain open until the in-flight mutation released its read gate")
	}

	releaseRoom()
	select {
	case addErr := <-addResult:
		if addErr != nil {
			t.Fatalf("expected in-flight addPlayer to commit: %v", addErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for in-flight addPlayer")
	}
	if err := waitStoreShutdown(t, shutdownResult); err != nil {
		t.Fatalf("shutdown after shared mutation gate drain: %v", err)
	}
	store.mu.RLock()
	roomsAfterShutdown := len(store.rooms)
	playerIDsAfterShutdown := len(store.playerIDs)
	store.mu.RUnlock()
	if roomsAfterShutdown != 0 || playerIDsAfterShutdown != 0 {
		t.Fatalf("expected shutdown to clear registries, rooms=%d playerIDs=%d", roomsAfterShutdown, playerIDsAfterShutdown)
	}
}

func TestStartHandlerReturnsInternalErrorAfterStoreQuiesces(t *testing.T) {
	clock := newBlockingStopClock()
	store := NewStoreWithClock(5, clock)
	handler := debugHandler(t, store)
	t.Cleanup(func() {
		clock.ticker.release()
		store.Close()
	})

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add player: %v", err)
	}

	shutdownResult := startStoreShutdown(store, context.Background())
	waitShutdownSignal(t, clock.ticker.stopStarted, "handler shutdown quiescing barrier")
	recorder := request(handler, http.MethodPost, "/rooms/"+created.ID+"/start")
	assertInternalError(t, recorder)

	clock.ticker.release()
	if err := waitStoreShutdown(t, shutdownResult); err != nil {
		t.Fatalf("handler shutdown: %v", err)
	}
}

func TestStoreShutdownLeavesRoomsClientsAndReservationsAtZero(t *testing.T) {
	fakeClock := newFakeClock()
	observer := &recordingObserver{}
	store := newStore(5, fakeClock, StoreConfig{Observer: observer})
	t.Cleanup(store.Close)

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	connectedPlayer, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add connected player: %v", err)
	}
	reservedPlayer, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add reserved player: %v", err)
	}
	conn := newFakeClientConn(false)
	session := attachHeartbeatTestSession(t, store, created.ID, connectedPlayer.Player.ID, connectedPlayer.SessionToken, conn)
	if _, err := store.reserveClient(created.ID, reservedPlayer.Player.ID, []string{reservedPlayer.SessionToken}); err != nil {
		t.Fatalf("reserve second player: %v", err)
	}

	capturedRoom := store.lookupRoom(created.ID)
	before := captureShutdownMutationState(store, capturedRoom)
	if before.rooms != 1 || before.players != 2 || before.clients != 1 || before.reservations != 1 || before.playerIDs != 2 || before.activeSessions != 1 {
		t.Fatalf("shutdown fixture did not cover every registry: %+v", before)
	}
	store.mu.RLock()
	lifecycleDone := store.activeSessions[session]
	store.mu.RUnlock()
	if lifecycleDone == nil {
		t.Fatal("expected attached session lifecycle registration")
	}

	if err := store.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown store: %v", err)
	}

	after := captureShutdownMutationState(store, capturedRoom)
	if after.rooms != 0 || after.clients != 0 || after.reservations != 0 || after.playerIDs != 0 || after.activeSessions != 0 || !after.removed {
		t.Fatalf("expected shutdown to drain every registry, got %+v", after)
	}
	assertShutdownGracefulClose(t, conn)
	if got := conn.forceCount.Load(); got != 0 {
		t.Fatalf("expected normal zero-state shutdown not to force close, got %d", got)
	}
	assertShutdownChannelClosed(t, session.closeDone, "session closeDone")
	assertShutdownChannelClosed(t, session.writerDone, "session writerDone")
	assertShutdownChannelClosed(t, session.heartbeatDone, "session heartbeatDone")
	assertShutdownChannelClosed(t, lifecycleDone, "session lifecycleDone")
	assertShutdownObserverEndsAtZero(t, observer.activeRoomValues(), "active rooms")
	assertShutdownObserverEndsAtZero(t, observer.connectedClientValues(), "connected clients")
}

func TestStoreShutdownClosesSessionsInParallel(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	allowClose := make(chan struct{})
	var releaseClose sync.Once
	release := func() {
		releaseClose.Do(func() { close(allowClose) })
	}
	t.Cleanup(func() {
		release()
		store.Close()
	})

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	const sessionCount = 3
	connections := make([]*fakeClientConn, 0, sessionCount)
	sessions := make([]*clientSession, 0, sessionCount)
	lifecycleDone := make([]<-chan struct{}, 0, sessionCount)
	for index := range sessionCount {
		issued, addErr := store.addPlayer(created.ID)
		if addErr != nil {
			t.Fatalf("add player %d: %v", index, addErr)
		}
		conn := newFakeClientConn(false)
		conn.closeStarted = make(chan struct{})
		conn.closeBlock = allowClose
		session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)

		store.mu.RLock()
		done := store.activeSessions[session]
		store.mu.RUnlock()
		if done == nil {
			t.Fatalf("expected lifecycle registration for session %d", index)
		}
		connections = append(connections, conn)
		sessions = append(sessions, session)
		lifecycleDone = append(lifecycleDone, done)
	}

	shutdownResult := startStoreShutdown(store, context.Background())
	for index, conn := range connections {
		waitShutdownSignal(t, conn.closeStarted, fmt.Sprintf("parallel close entry %d", index))
	}
	for index, conn := range connections {
		if got := conn.closeCount.Load(); got != 1 {
			t.Fatalf("expected session %d to enter graceful close once before release, got %d", index, got)
		}
	}

	release()
	if err := waitStoreShutdown(t, shutdownResult); err != nil {
		t.Fatalf("parallel shutdown: %v", err)
	}
	for index, conn := range connections {
		assertShutdownGracefulClose(t, conn)
		if got := conn.forceCount.Load(); got != 0 {
			t.Fatalf("expected parallel session %d not to force close, got %d", index, got)
		}
	}
	for index, session := range sessions {
		assertShutdownChannelClosed(t, session.closeDone, fmt.Sprintf("session %d closeDone", index))
		assertShutdownChannelClosed(t, session.writerDone, fmt.Sprintf("session %d writerDone", index))
		assertShutdownChannelClosed(t, session.heartbeatDone, fmt.Sprintf("session %d heartbeatDone", index))
		assertShutdownChannelClosed(t, lifecycleDone[index], fmt.Sprintf("session %d lifecycleDone", index))
	}
}

func TestStoreShutdownForceClosesBlockingHandshakeAtDeadline(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	allowClose := make(chan struct{})
	var releaseClose sync.Once
	release := func() {
		releaseClose.Do(func() { close(allowClose) })
	}
	t.Cleanup(func() {
		release()
		store.Close()
	})

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	conn := newFakeClientConn(false)
	conn.closeStarted = make(chan struct{})
	conn.closeBlock = allowClose
	conn.forceFn = func() error {
		release()
		return nil
	}
	session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)
	store.mu.RLock()
	lifecycleDone := store.activeSessions[session]
	store.mu.RUnlock()
	if lifecycleDone == nil {
		t.Fatal("expected attached session lifecycle registration")
	}

	ctx := newShutdownManualDeadlineContext()
	shutdownResult := startStoreShutdown(store, ctx)
	waitShutdownSignal(t, conn.closeStarted, "blocking graceful close entry")
	ctx.expire()

	shutdownErr := waitStoreShutdown(t, shutdownResult)
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("expected shared shutdown deadline, got %v", shutdownErr)
	}
	assertShutdownGracefulClose(t, conn)
	if got := conn.forceCount.Load(); got != 1 {
		t.Fatalf("expected one force close to release graceful handshake, got %d", got)
	}
	assertShutdownChannelClosed(t, session.closeDone, "forced session closeDone")
	assertShutdownChannelClosed(t, session.writerDone, "forced session writerDone")
	assertShutdownChannelClosed(t, session.heartbeatDone, "forced session heartbeatDone")
	assertShutdownChannelClosed(t, lifecycleDone, "forced session lifecycleDone")
}

func TestStoreShutdownDeadlineAbortsRealWebSocketAlreadyClosing(t *testing.T) {
	store := NewStore(5)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	client := dialIssuedPlayer(t, server.URL, issued.WebSocketPath)
	defer client.CloseNow()

	store.mu.RLock()
	var session *clientSession
	var lifecycleDone <-chan struct{}
	for current, done := range store.activeSessions {
		session = current
		lifecycleDone = done
	}
	store.mu.RUnlock()
	if session == nil || lifecycleDone == nil {
		t.Fatal("expected real WebSocket session lifecycle registration")
	}

	ctx := newShutdownManualDeadlineContext()
	shutdownResult := startStoreShutdown(store, ctx)
	waitShutdownSignal(t, session.transportCloseStart, "real WebSocket graceful close entry")
	ctx.expire()

	select {
	case shutdownErr := <-shutdownResult:
		if !errors.Is(shutdownErr, context.DeadlineExceeded) {
			t.Fatalf("expected real WebSocket shutdown deadline, got %v", shutdownErr)
		}
	case <-time.After(time.Second):
		_ = client.CloseNow()
		shutdownErr := waitStoreShutdown(t, shutdownResult)
		t.Fatalf("shutdown deadline did not abort the real WebSocket transport; eventual result: %v", shutdownErr)
	}

	assertShutdownChannelClosed(t, session.closeDone, "real WebSocket session closeDone")
	assertShutdownChannelClosed(t, session.writerDone, "real WebSocket session writerDone")
	assertShutdownChannelClosed(t, session.heartbeatDone, "real WebSocket session heartbeatDone")
	assertShutdownChannelClosed(t, lifecycleDone, "real WebSocket session lifecycleDone")
}

func TestStoreShutdownForceDetachesTerminalRoomAlreadyInsideClose(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	allowClose := make(chan struct{})
	var releaseClose sync.Once
	release := func() {
		releaseClose.Do(func() { close(allowClose) })
	}
	t.Cleanup(func() {
		release()
		store.Close()
	})

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
	conn.closeStarted = make(chan struct{})
	conn.closeBlock = allowClose
	conn.forceFn = func() error {
		release()
		return nil
	}
	session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)
	store.mu.RLock()
	lifecycleDone := store.activeSessions[session]
	store.mu.RUnlock()
	if lifecycleDone == nil {
		t.Fatal("expected terminal session lifecycle registration")
	}

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
	waitShutdownSignal(t, conn.closeStarted, "terminal connection close entry")
	if got := store.lookupRoom(created.ID); got != room {
		t.Fatal("expected terminal room to remain registered while close barrier is blocked")
	}
	store.mu.RLock()
	activeBeforeShutdown := len(store.activeSessions)
	store.mu.RUnlock()
	if activeBeforeShutdown != 1 {
		t.Fatalf("expected terminal session to remain active while Close blocks, got %d", activeBeforeShutdown)
	}

	ctx, cancel := context.WithCancel(context.Background())
	shutdownResult := startStoreShutdown(store, ctx)
	cancel()
	shutdownErr := waitStoreShutdown(t, shutdownResult)
	if !errors.Is(shutdownErr, context.Canceled) {
		t.Fatalf("expected shared terminal shutdown cancellation, got %v", shutdownErr)
	}
	if got := conn.forceCount.Load(); got != 1 {
		t.Fatalf("expected one force close for terminal session, got %d", got)
	}
	if got := store.lookupRoom(created.ID); got != nil {
		t.Fatal("expected shutdown takeover to detach the terminal room")
	}
	assertShutdownChannelClosed(t, session.closeDone, "terminal session closeDone")
	assertShutdownChannelClosed(t, session.writerDone, "terminal session writerDone")
	assertShutdownChannelClosed(t, session.heartbeatDone, "terminal session heartbeatDone")
	assertShutdownChannelClosed(t, lifecycleDone, "terminal session lifecycleDone")
	store.mu.RLock()
	activeAfterShutdown := len(store.activeSessions)
	store.mu.RUnlock()
	if activeAfterShutdown != 0 {
		t.Fatalf("expected terminal session registry to drain, got %d", activeAfterShutdown)
	}
}

func TestShutdownIsForcedExceptionToGameEndCloseBarrier(t *testing.T) {
	logs := &lockedLogBuffer{}
	harness := newModeTickHarnessWithConfig(t, simulation.GameModeSolo, StoreConfig{
		Logger: jsonTestLogger(logs),
	}, nil, 0, 1, 2, 3, 4, 5)
	harness.setSnapshots(t, harness.snapshot(1, 0, 1, 2, 3, 4))
	closeStarted, releaseClose := harness.blockClose(t, 5)
	lifecycleDone := make([]<-chan struct{}, len(harness.sessions))
	harness.store.mu.RLock()
	for index, session := range harness.sessions {
		lifecycleDone[index] = harness.store.activeSessions[session]
	}
	harness.store.mu.RUnlock()
	for index, done := range lifecycleDone {
		if done == nil {
			t.Fatalf("expected terminal session %d lifecycle registration", index)
		}
	}
	harness.connections[5].forceFn = func() error {
		releaseClose()
		return nil
	}

	harness.store.tickRoomState(harness.room)
	waitShutdownSignal(t, closeStarted, "terminal winner connection close entry")
	select {
	case <-harness.sessions[5].closeDone:
		t.Fatal("expected terminal closeDone to remain open at the GameEnd barrier")
	default:
	}
	select {
	case <-harness.room.gameEndCleanupWorkerDone:
		t.Fatal("expected GameEnd cleanup worker to remain active while terminal close was blocked")
	default:
	}

	ctx := newShutdownManualDeadlineContext()
	shutdownResult := startStoreShutdown(harness.store, ctx)
	waitShutdownCondition(t, "forced Shutdown registry and player ID detach", func() bool {
		harness.store.mu.RLock()
		roomDetached := harness.store.rooms[harness.room.ID] == nil
		playerIDsReleased := len(harness.store.playerIDs) == 0
		harness.store.mu.RUnlock()
		return roomDetached && playerIDsReleased
	})
	select {
	case <-harness.sessions[5].closeDone:
		t.Fatal("expected Shutdown to detach registry ownership while terminal close remained blocked")
	default:
	}
	select {
	case err := <-shutdownResult:
		t.Fatalf("Shutdown returned before the blocked terminal close was forced: %v", err)
	default:
	}
	select {
	case <-harness.room.gameEndCleanupDone:
		t.Fatal("expected forced Shutdown not to signal normal GameEnd cleanup")
	default:
	}
	assertLogEventCount(t, logs, "room_ended", 0)

	ctx.expire()
	if err := waitStoreShutdown(t, shutdownResult); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected forced terminal Shutdown deadline, got %v", err)
	}
	if got := harness.connections[5].forceCount.Load(); got != 1 {
		t.Fatalf("expected one forced terminal transport close, got %d", got)
	}
	for index, session := range harness.sessions {
		assertShutdownChannelClosed(t, session.closeDone, fmt.Sprintf("terminal session %d closeDone", index))
		assertShutdownChannelClosed(t, session.writerDone, fmt.Sprintf("terminal session %d writerDone", index))
		assertShutdownChannelClosed(t, session.heartbeatDone, fmt.Sprintf("terminal session %d heartbeatDone", index))
		assertShutdownChannelClosed(t, lifecycleDone[index], fmt.Sprintf("terminal session %d lifecycleDone", index))
	}
	assertShutdownChannelClosed(t, harness.room.gameEndCleanupWorkerDone, "GameEnd cleanup workerDone")
	harness.store.mu.RLock()
	activeSessions := len(harness.store.activeSessions)
	rooms := len(harness.store.rooms)
	playerIDs := len(harness.store.playerIDs)
	harness.store.mu.RUnlock()
	if activeSessions != 0 || rooms != 0 || playerIDs != 0 {
		t.Fatalf("expected forced Shutdown to drain registries, activeSessions=%d rooms=%d playerIDs=%d",
			activeSessions, rooms, playerIDs)
	}
	select {
	case <-harness.room.gameEndCleanupDone:
		t.Fatal("expected forced Shutdown to leave normal GameEnd cleanup incomplete")
	default:
	}
	assertLogEventCount(t, logs, "room_ended", 0)
}

func TestStoreShutdownCancellationForcesBeforeRoomTickerStopJoins(t *testing.T) {
	clock := newBlockingStopClock()
	store := NewStoreWithClock(5, clock)
	allowClose := make(chan struct{})
	var releaseClose sync.Once
	releaseConnection := func() {
		releaseClose.Do(func() { close(allowClose) })
	}
	t.Cleanup(func() {
		clock.ticker.release()
		releaseConnection()
		store.Close()
	})

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
	conn.closeBlock = allowClose
	conn.forceFn = func() error {
		releaseConnection()
		return nil
	}
	attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)

	ctx, cancel := context.WithCancel(context.Background())
	shutdownResult := startStoreShutdown(store, ctx)
	waitShutdownSignal(t, clock.ticker.stopStarted, "started-room janitor stop")
	cancel()
	waitShutdownCondition(t, "started-room force before ticker stop join", func() bool {
		return conn.forceCount.Load() == 1
	})
	select {
	case err := <-shutdownResult:
		t.Fatalf("shutdown returned before blocking ticker stops joined: %v", err)
	default:
	}

	clock.ticker.release()
	if err := waitStoreShutdown(t, shutdownResult); !errors.Is(err, context.Canceled) {
		t.Fatalf("started-room canceled shutdown: %v", err)
	}
	assertShutdownGracefulClose(t, conn)
}

func TestStoreShutdownCancellationUsesShutdownReasonForEveryPreStartSession(t *testing.T) {
	clock := newBlockingStopClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(func() {
		clock.ticker.release()
		store.Close()
	})

	first, err := store.joinMatchmaking(store.defaultGameMode())
	if err != nil {
		t.Fatalf("first matchmaking join: %v", err)
	}
	second, err := store.joinMatchmaking(store.defaultGameMode())
	if err != nil {
		t.Fatalf("second matchmaking join: %v", err)
	}
	if second.Room.ID != first.Room.ID {
		t.Fatalf("expected pre-start players in one room, got %q and %q", first.Room.ID, second.Room.ID)
	}

	connections := []*fakeClientConn{newFakeClientConn(false), newFakeClientConn(false)}
	players := []matchmakingJoinResponse{first, second}
	for index, player := range players {
		attachHeartbeatTestSession(
			t,
			store,
			player.Room.ID,
			player.Player.ID,
			player.SessionToken,
			connections[index],
		)
	}

	ctx, cancel := context.WithCancel(context.Background())
	shutdownResult := startStoreShutdown(store, ctx)
	waitShutdownSignal(t, clock.ticker.stopStarted, "pre-start cancellation janitor barrier")
	cancel()
	for index, conn := range connections {
		waitShutdownCondition(t, fmt.Sprintf("pre-start shutdown close %d", index), func() bool {
			code, reason := conn.closeMetadata()
			return conn.closeCount.Load() == 1 && conn.forceCount.Load() == 1 &&
				code == websocket.StatusNormalClosure && reason == shutdownWebSocketCloseReason
		})
		assertShutdownGracefulClose(t, conn)
	}

	clock.ticker.release()
	if err := waitStoreShutdown(t, shutdownResult); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-start canceled shutdown: %v", err)
	}
}

func TestStoreShutdownJoinsJanitorWriterHeartbeatAndLifecycleMonitor(t *testing.T) {
	clock := newBlockingStopClock()
	store := NewStoreWithClock(5, clock)
	allowClose := make(chan struct{})
	var releaseClose sync.Once
	releaseConnection := func() {
		releaseClose.Do(func() { close(allowClose) })
	}
	t.Cleanup(func() {
		clock.ticker.release()
		releaseConnection()
		store.Close()
	})

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	conn := newFakeClientConn(true)
	conn.closeStarted = make(chan struct{})
	conn.closeBlock = allowClose
	session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)
	store.mu.RLock()
	lifecycleDone := store.activeSessions[session]
	store.mu.RUnlock()
	if lifecycleDone == nil {
		t.Fatal("expected joined session lifecycle registration")
	}
	if !session.enqueueControl([]byte("blocking writer payload")) {
		t.Fatal("expected writer payload enqueue")
	}
	waitShutdownSignal(t, conn.writeStarted, "writer Write entry")

	shutdownResult := startStoreShutdown(store, context.Background())
	waitShutdownSignal(t, clock.ticker.stopStarted, "janitor Stop entry")
	select {
	case err := <-shutdownResult:
		t.Fatalf("shutdown returned before janitor joined: %v", err)
	default:
	}
	select {
	case <-store.janitorDone:
		t.Fatal("janitor finished before its Stop barrier was released")
	default:
	}
	select {
	case <-session.writerDone:
		t.Fatal("writer exited before shutdown advanced past janitor join")
	default:
	}

	clock.ticker.release()
	waitShutdownSignal(t, conn.closeStarted, "session graceful close entry")
	select {
	case err := <-shutdownResult:
		t.Fatalf("shutdown returned before connection and lifecycle joined: %v", err)
	default:
	}
	select {
	case <-session.closeDone:
		t.Fatal("session closeDone closed while Conn.Close remained blocked")
	default:
	}
	select {
	case <-session.writerDone:
		t.Fatal("writerDone closed before blocking closeOnce owner completed")
	default:
	}
	select {
	case <-lifecycleDone:
		t.Fatal("lifecycle monitor finished before close and writer joined")
	default:
	}

	releaseConnection()
	if err := waitStoreShutdown(t, shutdownResult); err != nil {
		t.Fatalf("joined shutdown: %v", err)
	}
	assertShutdownGracefulClose(t, conn)
	if got := conn.forceCount.Load(); got != 0 {
		t.Fatalf("expected normal joined shutdown not to force close, got %d", got)
	}
	assertShutdownChannelClosed(t, store.janitorDone, "janitorDone")
	assertShutdownChannelClosed(t, session.closeDone, "joined session closeDone")
	assertShutdownChannelClosed(t, session.writerDone, "joined session writerDone")
	assertShutdownChannelClosed(t, session.heartbeatDone, "joined session heartbeatDone")
	assertShutdownChannelClosed(t, lifecycleDone, "joined session lifecycleDone")
	store.mu.RLock()
	activeAfterShutdown := len(store.activeSessions)
	store.mu.RUnlock()
	if activeAfterShutdown != 0 {
		t.Fatalf("expected joined lifecycle registry to drain, got %d", activeAfterShutdown)
	}
}

func TestStoreShutdownJoinsRoomOwnedWorkers(t *testing.T) {
	t.Run("gameplay worker after room lock release", func(t *testing.T) {
		clock := newFakeClock()
		observer := newBlockingTickObserver()
		store := newStore(5, clock, StoreConfig{Observer: observer})
		defer store.Close()
		defer observer.release()

		created, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		if _, err := store.addPlayer(created.ID); err != nil {
			t.Fatalf("add player: %v", err)
		}
		if _, err := store.startRoom(created.ID); err != nil {
			t.Fatalf("start room: %v", err)
		}

		clock.TickTicker(time.Second/time.Duration(store.gameConfig.TickRate), 0)
		waitShutdownSignal(t, observer.entered, "gameplay worker tick publication")

		shutdownResult := startStoreShutdown(store, context.Background())
		assertShutdownStillWaiting(t, shutdownResult, "gameplay worker")
		observer.release()
		if err := waitStoreShutdown(t, shutdownResult); err != nil {
			t.Fatalf("shutdown after gameplay worker release: %v", err)
		}
	})

	t.Run("countdown worker after ticker handoff", func(t *testing.T) {
		clock := newFakeClock()
		logEntered := make(chan struct{})
		logRelease := make(chan struct{})
		var enterOnce sync.Once
		var releaseOnce sync.Once
		releaseLog := func() { releaseOnce.Do(func() { close(logRelease) }) }
		logger := slog.New(&callbackLogHandler{handle: func(record slog.Record) {
			if record.Message != "room_started" {
				return
			}
			enterOnce.Do(func() { close(logEntered) })
			<-logRelease
		}})
		store := newStore(5, clock, StoreConfig{Logger: logger})
		defer store.Close()
		defer releaseLog()

		created, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		if _, err := store.addPlayer(created.ID); err != nil {
			t.Fatalf("add player: %v", err)
		}
		room := store.lookupRoom(created.ID)
		room.mu.Lock()
		store.startMatchCountdownLocked(room)
		room.countdown = 1
		room.mu.Unlock()

		clock.TickTicker(time.Second, 0)
		waitShutdownSignal(t, logEntered, "countdown worker room_started publication")

		shutdownResult := startStoreShutdown(store, context.Background())
		assertShutdownStillWaiting(t, shutdownResult, "countdown worker")
		releaseLog()
		if err := waitStoreShutdown(t, shutdownResult); err != nil {
			t.Fatalf("shutdown after countdown worker release: %v", err)
		}
	})
}

func assertShutdownStillWaiting(t *testing.T, result <-chan error, worker string) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("shutdown returned before %s exited: %v", worker, err)
	case <-time.After(50 * time.Millisecond):
	}
}

type blockingTickObserver struct {
	entered     chan struct{}
	releaseTick chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingTickObserver() *blockingTickObserver {
	return &blockingTickObserver{
		entered:     make(chan struct{}),
		releaseTick: make(chan struct{}),
	}
}

func (*blockingTickObserver) SetActiveRooms(int) {}

func (*blockingTickObserver) SetConnectedClients(int) {}

func (o *blockingTickObserver) ObserveTick(time.Duration) {
	o.enterOnce.Do(func() { close(o.entered) })
	<-o.releaseTick
}

func (o *blockingTickObserver) release() {
	o.releaseOnce.Do(func() { close(o.releaseTick) })
}

func TestStoreShutdownIsIdempotentForConcurrentCallers(t *testing.T) {
	clock := newBlockingStopClock()
	store := NewStoreWithClock(5, clock)
	allowClose := make(chan struct{})
	var releaseClose sync.Once
	releaseConnection := func() {
		releaseClose.Do(func() { close(allowClose) })
	}
	t.Cleanup(func() {
		clock.ticker.release()
		releaseConnection()
		store.Close()
	})

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	conn := newFakeClientConn(false)
	conn.closeStarted = make(chan struct{})
	conn.closeBlock = allowClose
	conn.forceFn = func() error {
		releaseConnection()
		return nil
	}
	session := attachHeartbeatTestSession(t, store, created.ID, issued.Player.ID, issued.SessionToken, conn)
	store.mu.RLock()
	lifecycleDone := store.activeSessions[session]
	store.mu.RUnlock()
	if lifecycleDone == nil {
		t.Fatal("expected concurrent shutdown lifecycle registration")
	}

	cancelable, cancel := context.WithCancel(context.Background())
	const callerCount = 4
	results := make([]<-chan error, 0, callerCount)
	ownerContext := newShutdownObservedContext(cancelable)
	results = append(results, startStoreShutdown(store, ownerContext))
	waitShutdownSignal(t, ownerContext.doneCalled, "Shutdown owner wait entry")
	waitShutdownSignal(t, clock.ticker.stopStarted, "concurrent shutdown janitor Stop entry")

	var cancelFollower context.CancelFunc
	for index := 1; index < callerCount; index++ {
		followerContext := context.Background()
		if index == 1 {
			followerContext, cancelFollower = context.WithCancel(context.Background())
		}
		observed := newShutdownObservedContext(followerContext)
		results = append(results, startStoreShutdown(store, observed))
		waitShutdownSignal(t, observed.doneCalled, fmt.Sprintf("Shutdown caller %d wait entry", index))
	}
	for index, result := range results {
		select {
		case err := <-result:
			t.Fatalf("caller %d returned before shared shutdown completion: %v", index, err)
		default:
		}
	}
	cancelFollower()
	select {
	case err := <-results[1]:
		t.Fatalf("follower returned from its own cancellation before shared shutdown completion: %v", err)
	default:
	}
	if got := conn.forceCount.Load(); got != 0 {
		t.Fatalf("follower cancellation forced owner shutdown, force count=%d", got)
	}

	cancel()
	waitShutdownSignal(t, conn.closeStarted, "concurrent shutdown graceful close entry")
	waitShutdownCondition(t, "concurrent shutdown force close", func() bool {
		return conn.forceCount.Load() == 1
	})
	select {
	case <-store.janitorDone:
		t.Fatal("janitor joined before its blocking Stop was released")
	default:
	}
	for index, result := range results {
		select {
		case err := <-result:
			t.Fatalf("caller %d returned after force but before shared shutdown completion: %v", index, err)
		default:
		}
	}

	clock.ticker.release()
	for index, result := range results {
		shutdownErr := waitStoreShutdown(t, result)
		if !errors.Is(shutdownErr, context.Canceled) {
			t.Fatalf("caller %d expected shared context cancellation, got %v", index, shutdownErr)
		}
	}
	assertShutdownGracefulClose(t, conn)
	if got := conn.closeCount.Load(); got != 1 {
		t.Fatalf("expected one logical graceful close, got %d", got)
	}
	if got := conn.forceCount.Load(); got != 1 {
		t.Fatalf("expected one logical force close, got %d", got)
	}
	assertShutdownChannelClosed(t, session.closeDone, "concurrent session closeDone")
	assertShutdownChannelClosed(t, session.writerDone, "concurrent session writerDone")
	assertShutdownChannelClosed(t, session.heartbeatDone, "concurrent session heartbeatDone")
	assertShutdownChannelClosed(t, lifecycleDone, "concurrent session lifecycleDone")
	store.mu.RLock()
	activeAfterShutdown := len(store.activeSessions)
	roomsAfterShutdown := len(store.rooms)
	store.mu.RUnlock()
	if activeAfterShutdown != 0 || roomsAfterShutdown != 0 {
		t.Fatalf("expected shared shutdown registries to drain, rooms=%d activeSessions=%d", roomsAfterShutdown, activeAfterShutdown)
	}
	if err := store.Shutdown(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected completed Shutdown to return stored cancellation, got %v", err)
	}
}

func TestStoreShutdownDrainsBeforeSharingCallbackPanic(t *testing.T) {
	clock := newBlockingStopClock()
	panicValue := errors.New("shutdown observer panic sentinel")
	observer := &shutdownPanicObserver{panicValue: panicValue}
	store := newStore(5, clock, StoreConfig{Observer: observer})
	t.Cleanup(clock.ticker.release)

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add player: %v", err)
	}
	capturedRoom := store.lookupRoom(created.ID)

	ownerContext := newShutdownObservedContext(context.Background())
	ownerResult := startStoreShutdownOutcome(store, ownerContext)
	waitShutdownSignal(t, ownerContext.doneCalled, "panic shutdown owner wait entry")
	waitShutdownSignal(t, clock.ticker.stopStarted, "panic shutdown janitor barrier")
	followerContext := newShutdownObservedContext(context.Background())
	followerResult := startStoreShutdownOutcome(store, followerContext)
	waitShutdownSignal(t, followerContext.doneCalled, "panic shutdown follower wait entry")

	clock.ticker.release()
	for name, result := range map[string]<-chan shutdownOutcome{
		"owner":    ownerResult,
		"follower": followerResult,
	} {
		select {
		case outcome := <-result:
			if !outcome.panicked || outcome.panicValue != panicValue {
				t.Fatalf("%s expected shared callback panic %v, got %+v", name, panicValue, outcome)
			}
			if outcome.returned {
				t.Fatalf("%s returned normally despite shared callback panic: %v", name, outcome.err)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s panic outcome", name)
		}
	}

	after := captureShutdownMutationState(store, capturedRoom)
	if after.rooms != 0 || after.playerIDs != 0 || !after.removed {
		t.Fatalf("callback panic left shutdown registries behind: %+v", after)
	}
	assertShutdownChannelClosed(t, store.janitorDone, "panic shutdown janitorDone")
	assertShutdownObserverEndsAtZero(t, observer.activeRoomValues(), "panic active rooms")
}

type shutdownMutationState struct {
	rooms          int
	players        int
	clients        int
	reservations   int
	pendingInputs  int
	readyPlayers   int
	playerIDs      int
	activeSessions int
	status         RoomStatus
	removed        bool
}

type shutdownOutcome struct {
	err        error
	panicValue any
	panicked   bool
	returned   bool
}

type shutdownPanicObserver struct {
	recordingObserver
	panicValue any
	panicOnce  sync.Once
}

func (o *shutdownPanicObserver) SetActiveRooms(count int) {
	o.recordingObserver.SetActiveRooms(count)
	if count == 0 {
		o.panicOnce.Do(func() { panic(o.panicValue) })
	}
}

func captureShutdownMutationState(store *Store, capturedRoom *room) shutdownMutationState {
	store.mu.RLock()
	state := shutdownMutationState{
		rooms:          len(store.rooms),
		playerIDs:      len(store.playerIDs),
		activeSessions: len(store.activeSessions),
	}
	store.mu.RUnlock()
	if capturedRoom == nil {
		return state
	}
	capturedRoom.mu.Lock()
	state.players = len(capturedRoom.Players)
	state.clients = len(capturedRoom.clients)
	state.reservations = len(capturedRoom.reservations)
	state.pendingInputs = len(capturedRoom.pendingInputs)
	state.readyPlayers = len(capturedRoom.readyPlayers)
	state.status = capturedRoom.Status
	state.removed = capturedRoom.removed
	capturedRoom.mu.Unlock()
	return state
}

type shutdownBarrierReader struct {
	entered     chan struct{}
	releaseRead chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
	value       byte
}

func newShutdownBarrierReader(value byte) *shutdownBarrierReader {
	return &shutdownBarrierReader{
		entered:     make(chan struct{}),
		releaseRead: make(chan struct{}),
		value:       value,
	}
}

func (r *shutdownBarrierReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	r.enterOnce.Do(func() {
		close(r.entered)
		<-r.releaseRead
	})
	for index := range buffer {
		buffer[index] = r.value
	}
	return len(buffer), nil
}

func (r *shutdownBarrierReader) release() {
	r.releaseOnce.Do(func() { close(r.releaseRead) })
}

var _ io.Reader = (*shutdownBarrierReader)(nil)

type shutdownManualDeadlineContext struct {
	context.Context
	done       chan struct{}
	expireOnce sync.Once
}

func newShutdownManualDeadlineContext() *shutdownManualDeadlineContext {
	return &shutdownManualDeadlineContext{
		Context: context.Background(),
		done:    make(chan struct{}),
	}
}

func (c *shutdownManualDeadlineContext) Done() <-chan struct{} {
	return c.done
}

func (c *shutdownManualDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *shutdownManualDeadlineContext) expire() {
	c.expireOnce.Do(func() { close(c.done) })
}

type shutdownObservedContext struct {
	context.Context
	doneCalled chan struct{}
	doneOnce   sync.Once
}

func newShutdownObservedContext(ctx context.Context) *shutdownObservedContext {
	return &shutdownObservedContext{Context: ctx, doneCalled: make(chan struct{})}
}

func (c *shutdownObservedContext) Done() <-chan struct{} {
	c.doneOnce.Do(func() { close(c.doneCalled) })
	return c.Context.Done()
}

func startStoreShutdown(store *Store, ctx context.Context) <-chan error {
	result := make(chan error, 1)
	go func() {
		result <- store.Shutdown(ctx)
	}()
	return result
}

func startStoreShutdownOutcome(store *Store, ctx context.Context) <-chan shutdownOutcome {
	result := make(chan shutdownOutcome, 1)
	go func() {
		outcome := shutdownOutcome{}
		defer func() {
			if panicValue := recover(); panicValue != nil {
				outcome.panicValue = panicValue
				outcome.panicked = true
			}
			result <- outcome
		}()
		outcome.err = store.Shutdown(ctx)
		outcome.returned = true
	}()
	return result
}

func waitStoreShutdown(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Store.Shutdown")
		return fmt.Errorf("unreachable")
	}
}

func waitShutdownSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitShutdownCondition(t *testing.T, name string, condition func() bool) {
	t.Helper()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-timeout.C:
			t.Fatalf("timed out waiting for %s", name)
		default:
			runtime.Gosched()
		}
	}
}

func assertShutdownChannelClosed(t *testing.T, channel <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-channel:
	default:
		t.Fatalf("expected %s to be closed before Shutdown returns", name)
	}
}

func assertShutdownGracefulClose(t *testing.T, conn *fakeClientConn) {
	t.Helper()
	code, reason := conn.closeMetadata()
	if code != websocket.StatusNormalClosure || reason != "server shutting down" {
		t.Fatalf("expected shutdown graceful close 1000/server shutting down, got %d/%q", code, reason)
	}
}

func assertShutdownObserverEndsAtZero(t *testing.T, values []int, name string) {
	t.Helper()
	if len(values) == 0 || values[len(values)-1] != 0 {
		t.Fatalf("expected normal %s observer to end at zero, got %v", name, values)
	}
}
