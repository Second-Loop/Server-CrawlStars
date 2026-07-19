package rooms

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestObservationTransitionsDropLateStalePublish(t *testing.T) {
	observer := &recordingObserver{}
	state := newObservationState(observer)
	earlier := state.activeRoomsDelta(1)
	later := state.activeRoomsDelta(1)

	if earlier.sequence >= later.sequence {
		t.Fatalf("expected monotonic sequences, got earlier=%d later=%d", earlier.sequence, later.sequence)
	}
	state.publish(later)
	state.publish(earlier)

	if got := observer.activeRoomValues(); !slices.Equal(got, []int{2}) {
		t.Fatalf("expected stale value to be dropped after publishing 2, got %v", got)
	}
}

func TestObservationPublicationWaitsForItsObserverCallback(t *testing.T) {
	observer := newBlockingActiveRoomsObserver()
	state := newObservationState(observer)
	first := state.activeRoomsDelta(1)
	second := state.activeRoomsDelta(-1)

	firstDone := make(chan struct{})
	go func() {
		state.publish(first)
		close(firstDone)
	}()
	select {
	case <-observer.firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first observer callback did not start")
	}

	secondStarted := make(chan struct{})
	secondDone := make(chan struct{})
	go func() {
		close(secondStarted)
		state.publish(second)
		close(secondDone)
	}()
	<-secondStarted
	defer func() {
		select {
		case <-observer.releaseFirst:
		default:
			close(observer.releaseFirst)
		}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		state.publishMu.Lock()
		queuedBehindDrainer := state.draining && len(state.pending) > 0
		state.publishMu.Unlock()
		if queuedBehindDrainer {
			break
		}
		select {
		case <-secondDone:
			t.Fatal("publish returned before its queued observer callback completed")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("second observer publication was not queued")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-secondDone:
		t.Fatal("publish returned before its queued observer callback completed")
	default:
	}

	close(observer.releaseFirst)
	for name, done := range map[string]<-chan struct{}{
		"first publish":  firstDone,
		"second publish": secondDone,
	} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s did not complete after observer release", name)
		}
	}
	assertObserverValues(t, observer.activeRoomValues(), []int{1, 0})
}

func TestObservationCountersNeverGoNegativeOnDuplicateRelease(t *testing.T) {
	observer := &recordingObserver{}
	state := newObservationState(observer)
	transitions := []observationTransition{
		state.connectedClientsDelta(1),
		state.connectedClientsDelta(-1),
		state.connectedClientsDelta(-1),
	}

	for index, transition := range transitions {
		if transition.value < 0 {
			t.Fatalf("transition %d went negative: %+v", index, transition)
		}
		if index > 0 && transitions[index-1].sequence >= transition.sequence {
			t.Fatalf("expected monotonic sequences, got %d then %d", transitions[index-1].sequence, transition.sequence)
		}
		state.publish(transition)
	}

	if got := observer.connectedClientValues(); !slices.Equal(got, []int{1, 0, 0}) {
		t.Fatalf("expected duplicate release to stay at zero, got %v", got)
	}
}

func TestObservationNilObserverIsNormalizedToNoOp(t *testing.T) {
	state := newObservationState(nil)
	state.publish(state.activeRoomsDelta(1))
	state.publish(state.connectedClientsDelta(1))
	state.observeTick(time.Millisecond)
}

func TestObservationStateIsOwnedIndependentlyByEachStore(t *testing.T) {
	first := NewStore(1)
	second := NewStore(1)
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)

	if first.observation == nil || second.observation == nil {
		t.Fatal("expected every store to own observation state")
	}
	if first.observation == second.observation {
		t.Fatal("expected observation state to be isolated per store")
	}
	firstTransition := first.observation.activeRoomsDelta(1)
	secondTransition := second.observation.activeRoomsDelta(1)
	if firstTransition.value != 1 || firstTransition.sequence != 1 {
		t.Fatalf("unexpected first store transition: %+v", firstTransition)
	}
	if secondTransition.value != 1 || secondTransition.sequence != 1 {
		t.Fatalf("unexpected second store transition: %+v", secondTransition)
	}
}

func TestActiveRoomGaugeCoversCreateDeleteExpiryCancelGameEndClearAndClose(t *testing.T) {
	t.Run("create and delete", func(t *testing.T) {
		observer := &recordingObserver{}
		store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
		t.Cleanup(store.Close)
		created, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		if _, deleted := store.deleteRoom(created.ID); !deleted {
			t.Fatal("expected room deletion")
		}
		assertObserverValues(t, observer.activeRoomValues(), []int{1, 0})
	})

	t.Run("expiry", func(t *testing.T) {
		observer := &recordingObserver{}
		clock := newFakeClock()
		store := newStore(5, clock, StoreConfig{Observer: observer})
		t.Cleanup(store.Close)
		if _, err := store.createRoom(); err != nil {
			t.Fatalf("create room: %v", err)
		}
		clock.Advance(defaultWaitingRoomIdleTTL)
		if deleted := store.cleanupExpired(clock.Now()); deleted != 1 {
			t.Fatalf("expected one expiry, got %d", deleted)
		}
		assertObserverValues(t, observer.activeRoomValues(), []int{1, 0})
	})

	t.Run("pre-start cancel", func(t *testing.T) {
		observer := &recordingObserver{}
		store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
		t.Cleanup(store.Close)
		first, err := store.joinMatchmaking(store.defaultGameMode())
		if err != nil {
			t.Fatalf("first matchmaking join: %v", err)
		}
		if _, err := store.joinMatchmaking(store.defaultGameMode()); err != nil {
			t.Fatalf("second matchmaking join: %v", err)
		}
		reservation, err := store.reserveClient(first.Room.ID, first.Player.ID, []string{first.SessionToken})
		if err != nil {
			t.Fatalf("reserve client: %v", err)
		}
		session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
		if !attached {
			t.Fatal("expected client attach")
		}
		session.close(1000, "test close")
		assertObserverValues(t, observer.activeRoomValues(), []int{1, 0})
	})

	t.Run("game end", func(t *testing.T) {
		observer := &recordingObserver{}
		store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
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
		waitForGameEndCleanup(t, room)
		assertObserverValues(t, observer.activeRoomValues(), []int{1, 0})
	})

	t.Run("clear", func(t *testing.T) {
		observer := &recordingObserver{}
		store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
		t.Cleanup(store.Close)
		for range 2 {
			if _, err := store.createRoom(); err != nil {
				t.Fatalf("create room: %v", err)
			}
		}
		if cleared := store.clearRooms(); cleared.Deleted != 2 {
			t.Fatalf("expected two cleared rooms, got %d", cleared.Deleted)
		}
		assertObserverValues(t, observer.activeRoomValues(), []int{1, 2, 1, 0})
	})

	t.Run("store close", func(t *testing.T) {
		observer := &recordingObserver{}
		store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
		for range 2 {
			if _, err := store.createRoom(); err != nil {
				t.Fatalf("create room: %v", err)
			}
		}
		store.Close()
		assertObserverValues(t, observer.activeRoomValues(), []int{1, 2, 1, 0})
	})
}

func TestTerminalResourcesStopBeforeObserver(t *testing.T) {
	observer := newBlockingTickObserver()
	t.Cleanup(observer.release)
	harness := newModeTickHarness(t, simulation.GameModeSolo, observer, nil)
	harness.setSnapshots(t, harness.snapshot(1, 0, 1, 2, 3, 4, 5))
	harness.room.mu.Lock()
	gameplayTicker := harness.room.ticker.(*fakeTicker)
	gameplayStop := harness.room.stop
	harness.room.mu.Unlock()

	tickDone := make(chan struct{})
	go func() {
		harness.store.tickRoomState(harness.room)
		close(tickDone)
	}()
	select {
	case <-observer.entered:
	case <-time.After(time.Second):
		t.Fatal("expected terminal tick observer to be entered")
	}
	if got := gameplayTicker.StopCount(); got != 1 {
		t.Fatalf("expected gameplay ticker to stop before observer, got %d stops", got)
	}
	select {
	case <-gameplayStop:
	default:
		t.Fatal("expected gameplay stop channel to close before observer")
	}

	observer.release()
	select {
	case <-tickDone:
	case <-time.After(time.Second):
		t.Fatal("expected terminal tick to finish after observer release")
	}
	waitForGameEndCleanup(t, harness.room)
}

func TestGameEndCleanupSignalRequiresSuccessfulCallbacks(t *testing.T) {
	observer := &oneShotPanickingActiveRoomObserver{}
	harness := newModeTickHarness(t, simulation.GameModeSolo, observer, nil)
	var gameplay roomResources
	harness.room.mu.Lock()
	harness.room.ending = true
	gameplay.detachGameplayLocked(harness.room)
	harness.room.mu.Unlock()
	gameplay.stop()

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		harness.store.finishGameEnd(harness.room)
	}()
	harness.store.observation.observer = noopObserver{}

	if recovered == nil {
		t.Fatal("expected active-room observer panic to propagate from finishGameEnd")
	}
	select {
	case <-harness.room.gameEndCleanupDone:
		t.Fatal("expected cleanup completion to remain open after callback panic")
	default:
	}
}

func TestConnectedClientGaugeCoversAttachFailureReconnectDetachAndStoreClose(t *testing.T) {
	t.Run("auth and attach failure", func(t *testing.T) {
		observer := &recordingObserver{}
		store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
		t.Cleanup(store.Close)
		created, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		issued, err := store.addPlayer(created.ID)
		if err != nil {
			t.Fatalf("add player: %v", err)
		}
		if _, err := store.reserveClient(created.ID, issued.Player.ID, []string{"wrong"}); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected unauthorized reservation, got %v", err)
		}
		reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
		if err != nil {
			t.Fatalf("reserve client: %v", err)
		}
		if _, deleted := store.deleteRoom(created.ID); !deleted {
			t.Fatal("expected room deletion")
		}
		if _, attached := store.attachClientSession(reservation, newFakeClientConn(false)); attached {
			t.Fatal("expected stale reservation attach to fail")
		}
		assertObserverValues(t, observer.connectedClientValues(), nil)
	})

	t.Run("detach reconnect and stale close", func(t *testing.T) {
		observer := &recordingObserver{}
		store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
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

		firstReservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
		if err != nil {
			t.Fatalf("reserve first client: %v", err)
		}
		first, attached := store.attachClientSession(firstReservation, newFakeClientConn(false))
		if !attached {
			t.Fatal("expected first attach")
		}
		if _, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken}); !errors.Is(err, ErrPlayerAlreadyConnected) {
			t.Fatalf("expected duplicate connection rejection, got %v", err)
		}
		first.close(1000, "first close")

		secondReservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
		if err != nil {
			t.Fatalf("reserve reconnect: %v", err)
		}
		second, attached := store.attachClientSession(secondReservation, newFakeClientConn(false))
		if !attached {
			t.Fatal("expected reconnect attach")
		}
		first.close(1000, "stale duplicate close")
		second.close(1000, "second close")

		assertObserverValues(t, observer.connectedClientValues(), []int{1, 0, 1, 0})
	})

	t.Run("bulk store close", func(t *testing.T) {
		observer := &recordingObserver{}
		store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
		created, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		players := make([]playerSessionResponse, 0, 2)
		for range 2 {
			issued, err := store.addPlayer(created.ID)
			if err != nil {
				t.Fatalf("add player: %v", err)
			}
			players = append(players, issued)
		}
		if _, err := store.startRoom(created.ID); err != nil {
			t.Fatalf("start room: %v", err)
		}
		sessions := make([]*clientSession, 0, len(players))
		for _, player := range players {
			reservation, err := store.reserveClient(created.ID, player.Player.ID, []string{player.SessionToken})
			if err != nil {
				t.Fatalf("reserve client: %v", err)
			}
			session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
			if !attached {
				t.Fatal("expected client attach")
			}
			sessions = append(sessions, session)
		}

		store.Close()
		for _, session := range sessions {
			session.close(1000, "late duplicate close")
		}
		assertObserverValues(t, observer.connectedClientValues(), []int{1, 2, 1, 0})
	})

	t.Run("game end with connected client", func(t *testing.T) {
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
		reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
		if err != nil {
			t.Fatalf("reserve client: %v", err)
		}
		session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
		if !attached {
			t.Fatal("expected client attach")
		}
		room := store.lookupRoom(created.ID)
		room.mu.Lock()
		room.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
			return simulation.Snapshot{Players: []simulation.PlayerData{{
				ID:     simulation.PlayerID(issued.Player.ID),
				IsDead: true,
			}}}
		})
		room.mu.Unlock()

		store.tickRoomState(room)
		<-session.writerDone
		waitForGameEndCleanup(t, room)

		assertObserverValues(t, observer.connectedClientValues(), []int{1, 0})
		assertLogEventCount(t, logs, "websocket_connected", 1)
		assertLogEventCount(t, logs, "websocket_disconnected", 1)
		assertLogEventCount(t, logs, "room_ended", 1)
	})

	t.Run("stale room bulk detach", func(t *testing.T) {
		observer := &recordingObserver{}
		clock := newFakeClock()
		store := newStore(5, clock, StoreConfig{Observer: observer})
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
		if _, attached := store.attachClientSession(reservation, newFakeClientConn(false)); !attached {
			t.Fatal("expected client attach")
		}
		original := store.lookupRoom(created.ID)
		clock.Advance(defaultHardRoomLifetime)
		const staleSnapshotKey = "observation-stale-room"
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

		if deleted := store.cleanupExpired(clock.Now()); deleted != 0 {
			t.Fatalf("expected stale registry delete to fail, got %d", deleted)
		}
		assertObserverValues(t, observer.connectedClientValues(), []int{1, 0})
		assertObserverValues(t, observer.activeRoomValues(), []int{1})
	})
}

func TestObservationCallbacksRunOutsideCoreLocks(t *testing.T) {
	observer := &lockCheckingObserver{}
	store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
	observer.setStore(store)
	t.Cleanup(store.Close)

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	room := store.lookupRoom(created.ID)
	observer.addRoom(room)
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
	session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
	if !attached {
		t.Fatal("expected client attach")
	}
	store.mu.RLock()
	lifecycleDone := store.activeSessions[session]
	store.mu.RUnlock()
	if lifecycleDone == nil {
		t.Fatal("expected active session lifecycle")
	}
	session.close(1000, "test close")
	<-lifecycleDone
	if _, deleted := store.deleteRoom(created.ID); !deleted {
		t.Fatal("expected room deletion")
	}

	tickRoomResponse := createStartedRoomInStore(t, store)
	tickRoom := store.lookupRoom(tickRoomResponse.ID)
	observer.addRoom(tickRoom)
	tickRoom.mu.Lock()
	tickRoom.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
		return simulation.Snapshot{Players: []simulation.PlayerData{{
			ID: simulation.PlayerID(tickRoomResponse.Players[0].ID),
		}}}
	})
	tickRoom.mu.Unlock()
	store.tickRoomState(tickRoom)

	if failures := observer.failuresSnapshot(); len(failures) > 0 {
		t.Fatalf("observer callback ran under core lock: %v", failures)
	}
	if observer.tickCount() != 1 {
		t.Fatalf("expected one tick observation, got %d", observer.tickCount())
	}
}

func TestTickHistogramMeasuresOnlyStateStep(t *testing.T) {
	observer := &tickBoundaryObserver{}
	store := newStore(5, newFakeClock(), StoreConfig{Observer: observer})
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
	const stepDuration = 37 * time.Millisecond
	base := time.Unix(100, 0)
	wallCalls := 0
	timingOutsideRoomLock := false
	room := store.lookupRoom(created.ID)
	observer.room = room
	store.wallNow = func() time.Time {
		if room.mu.TryLock() {
			timingOutsideRoomLock = true
			room.mu.Unlock()
		}
		wallCalls++
		switch wallCalls {
		case 1:
			return base
		case 2:
			return base.Add(stepDuration)
		default:
			return base.Add(stepDuration)
		}
	}

	stepCalls := 0
	stepSawWallCalls := 0
	room.mu.Lock()
	room.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
		stepCalls++
		stepSawWallCalls = wallCalls
		return simulation.Snapshot{Players: []simulation.PlayerData{{
			ID: simulation.PlayerID(issued.Player.ID),
		}}}
	})
	room.mu.Unlock()

	store.tickRoomState(room)

	if timingOutsideRoomLock {
		t.Fatal("expected both wall-clock reads to occur while room.mu was held")
	}
	if stepCalls != 1 || stepSawWallCalls != 1 {
		t.Fatalf("expected one Step after the first wall-clock read, calls=%d wallCallsAtStep=%d", stepCalls, stepSawWallCalls)
	}
	if wallCalls != 2 {
		t.Fatalf("expected exactly two wall-clock reads around State.Step, got %d", wallCalls)
	}
	if observer.callbackWhileRoomLocked {
		t.Fatal("tick observer callback ran while room.mu was held")
	}
	if got := observer.tickDurations; !slices.Equal(got, []time.Duration{stepDuration}) {
		t.Fatalf("expected State.Step-only duration %v, got %v", stepDuration, got)
	}
}

func assertObserverValues(t *testing.T, got []int, want []int) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("expected observer values %v, got %v", want, got)
	}
}

type recordingObserver struct {
	mu               sync.Mutex
	activeRooms      []int
	connectedClients []int
	tickDurations    []time.Duration
}

type blockingActiveRoomsObserver struct {
	recordingObserver
	firstEntered chan struct{}
	releaseFirst chan struct{}
	firstOnce    sync.Once
}

type oneShotPanickingActiveRoomObserver struct {
	once sync.Once
}

func (o *oneShotPanickingActiveRoomObserver) SetActiveRooms(count int) {
	if count != 0 {
		return
	}
	o.once.Do(func() { panic("active-room observer panic") })
}

func (*oneShotPanickingActiveRoomObserver) SetConnectedClients(int) {}

func (*oneShotPanickingActiveRoomObserver) ObserveTick(time.Duration) {}

func newBlockingActiveRoomsObserver() *blockingActiveRoomsObserver {
	return &blockingActiveRoomsObserver{
		firstEntered: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

func (o *blockingActiveRoomsObserver) SetActiveRooms(count int) {
	o.recordingObserver.SetActiveRooms(count)
	if count != 1 {
		return
	}
	o.firstOnce.Do(func() {
		close(o.firstEntered)
		<-o.releaseFirst
	})
}

type tickBoundaryObserver struct {
	room                    *room
	tickDurations           []time.Duration
	callbackWhileRoomLocked bool
}

func (*tickBoundaryObserver) SetActiveRooms(int) {}

func (*tickBoundaryObserver) SetConnectedClients(int) {}

func (o *tickBoundaryObserver) ObserveTick(duration time.Duration) {
	if !o.room.mu.TryLock() {
		o.callbackWhileRoomLocked = true
	} else {
		o.room.mu.Unlock()
	}
	o.tickDurations = append(o.tickDurations, duration)
}

type lockCheckingObserver struct {
	mu       sync.Mutex
	store    *Store
	rooms    []*room
	failures []string
	ticks    int
}

func (o *lockCheckingObserver) setStore(store *Store) {
	o.mu.Lock()
	o.store = store
	o.mu.Unlock()
}

func (o *lockCheckingObserver) addRoom(room *room) {
	o.mu.Lock()
	o.rooms = append(o.rooms, room)
	o.mu.Unlock()
}

func (o *lockCheckingObserver) SetActiveRooms(count int) {
	o.checkCoreLocks(fmt.Sprintf("active rooms=%d", count))
}

func (o *lockCheckingObserver) SetConnectedClients(count int) {
	o.checkCoreLocks(fmt.Sprintf("connected clients=%d", count))
}

func (o *lockCheckingObserver) ObserveTick(time.Duration) {
	o.checkCoreLocks("tick")
	o.mu.Lock()
	o.ticks++
	o.mu.Unlock()
}

func (o *lockCheckingObserver) checkCoreLocks(callback string) {
	o.mu.Lock()
	store := o.store
	rooms := slices.Clone(o.rooms)
	o.mu.Unlock()
	if store != nil {
		if !store.mu.TryRLock() {
			o.recordFailure(callback + " held Store.mu")
		} else {
			store.mu.RUnlock()
		}
	}
	for _, room := range rooms {
		if room == nil {
			continue
		}
		if !room.mu.TryLock() {
			o.recordFailure(callback + " held room.mu")
			continue
		}
		room.mu.Unlock()
	}
}

func (o *lockCheckingObserver) recordFailure(failure string) {
	o.mu.Lock()
	o.failures = append(o.failures, failure)
	o.mu.Unlock()
}

func (o *lockCheckingObserver) failuresSnapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.failures)
}

func (o *lockCheckingObserver) tickCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ticks
}

func (o *recordingObserver) SetActiveRooms(count int) {
	o.mu.Lock()
	o.activeRooms = append(o.activeRooms, count)
	o.mu.Unlock()
}

func (o *recordingObserver) SetConnectedClients(count int) {
	o.mu.Lock()
	o.connectedClients = append(o.connectedClients, count)
	o.mu.Unlock()
}

func (o *recordingObserver) ObserveTick(duration time.Duration) {
	o.mu.Lock()
	o.tickDurations = append(o.tickDurations, duration)
	o.mu.Unlock()
}

func (o *recordingObserver) activeRoomValues() []int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.activeRooms)
}

func (o *recordingObserver) connectedClientValues() []int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.connectedClients)
}

func (o *recordingObserver) tickDurationValues() []time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.tickDurations)
}
