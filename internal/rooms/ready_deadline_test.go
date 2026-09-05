package rooms

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestLoadingReadyQuorumImmediatelyBeforeDeadlineStartsOnce(t *testing.T) {
	store, clock, room, sessions := loadingReadyDeadlineFixture(t)
	readyTicker := latestFakeTickerForDuration(t, clock, loadingReadyDeadline)

	store.markClientReady(room.ID, room.Players[0].ID, sessions[0])
	clock.Advance(loadingReadyDeadline - time.Nanosecond)
	store.markClientReady(room.ID, room.Players[1].ID, sessions[1])

	room.mu.Lock()
	status := room.matchStatus
	countdown := room.countdown
	retainedTicker := room.readyDeadlineTicker
	retainedStop := room.readyDeadlineStop
	deadlineAt := room.readyDeadlineAt
	room.mu.Unlock()
	if status != MatchStatusStarting || countdown != matchCountdownSeconds {
		t.Fatalf("pre-boundary quorum status=%q countdown=%d", status, countdown)
	}
	if retainedTicker != nil || retainedStop != nil || !deadlineAt.IsZero() {
		t.Fatalf("Starting retained ready deadline: ticker=%v stop=%v at=%v", retainedTicker, retainedStop, deadlineAt)
	}
	if got := readyTicker.StopCount(); got != 1 {
		t.Fatalf("ready ticker stops=%d want=1", got)
	}

	clock.Advance(time.Nanosecond)
	store.expireReadyDeadline(room, readyTicker)
	if store.lookupRoom(room.ID) != room {
		t.Fatal("stale ready deadline removed Starting room")
	}
}

func TestLoadingReadyQuorumIgnoresBotAck(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join human: %v", err)
	}
	room := store.lookupRoom(joined.Room.ID)
	reservation, err := store.reserveClient(joined.Room.ID, joined.Player.ID, []string{joined.SessionToken})
	if err != nil {
		t.Fatalf("reserve human: %v", err)
	}
	session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
	if !attached {
		t.Fatal("attach human")
	}
	room.mu.Lock()
	fillTicker := room.botFillTicker
	room.mu.Unlock()
	store.fillMatchmakingBots(room, fillTicker)

	store.markClientReady(room.ID, joined.Player.ID, session)
	room.mu.Lock()
	status := room.matchStatus
	readyCount := len(room.readyPlayers)
	players := append([]playerResponse(nil), room.Players...)
	room.mu.Unlock()
	if status != MatchStatusStarting || readyCount != 1 || len(players) != 2 || !players[1].IsBot {
		t.Fatalf("human+bot quorum status=%q ready=%d players=%+v", status, readyCount, players)
	}
}

func TestLoadingEntryWithAllHumanReadyStartsWithoutLiveDeadline(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	joined := make([]matchmakingJoinResponse, 0, 2)
	for range 2 {
		response, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
		if err != nil {
			t.Fatalf("join human: %v", err)
		}
		joined = append(joined, response)
	}
	room := store.lookupRoom(joined[0].Room.ID)
	for index, response := range joined {
		reservation, err := store.reserveClient(response.Room.ID, response.Player.ID, []string{response.SessionToken})
		if err != nil {
			t.Fatalf("reserve human %d: %v", index, err)
		}
		if index == 1 {
			room.mu.Lock()
			room.readyPlayers[response.Player.ID] = true
			room.mu.Unlock()
		}
		session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
		if !attached {
			t.Fatalf("attach human %d", index)
		}
		if index == 0 {
			store.markClientReady(room.ID, response.Player.ID, session)
		}
	}

	room.mu.Lock()
	status := room.matchStatus
	readyTicker := room.readyDeadlineTicker
	readyStop := room.readyDeadlineStop
	room.mu.Unlock()
	if status != MatchStatusStarting || readyTicker != nil || readyStop != nil {
		t.Fatalf("all-ready Loading entry status=%q ticker=%v stop=%v", status, readyTicker, readyStop)
	}
}

func TestLoadingReadyDeadlineCancelsWholeMatchAtExactBoundary(t *testing.T) {
	store, clock, room, sessions := loadingReadyDeadlineFixture(t)

	store.markClientReady(room.ID, room.Players[0].ID, sessions[0])
	clock.Advance(30 * time.Second)
	readyTicker := latestFakeTickerForDuration(t, clock, 30*time.Second)
	readyTicker.tick()
	waitForRoomDeleted(t, store, room.ID)

	waitShutdownCondition(t, "ready deadline player ID release", func() bool {
		store.mu.RLock()
		defer store.mu.RUnlock()
		return len(store.playerIDs) == 0
	})
	for index, session := range sessions {
		select {
		case <-session.done:
		case <-time.After(time.Second):
			t.Fatalf("session %d remained open after ready deadline", index)
		}
	}
}

func loadingReadyDeadlineFixture(t *testing.T) (*Store, *fakeClock, *room, []*clientSession) {
	t.Helper()
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	joined := make([]matchmakingJoinResponse, 0, 2)
	for range 2 {
		response, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
		if err != nil {
			t.Fatalf("join human: %v", err)
		}
		joined = append(joined, response)
	}
	room := store.lookupRoom(joined[0].Room.ID)
	sessions := make([]*clientSession, 0, len(joined))
	for _, response := range joined {
		reservation, err := store.reserveClient(response.Room.ID, response.Player.ID, []string{response.SessionToken})
		if err != nil {
			t.Fatalf("reserve human %s: %v", response.Player.ID, err)
		}
		session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
		if !attached {
			t.Fatalf("attach human %s", response.Player.ID)
		}
		sessions = append(sessions, session)
	}
	room.mu.Lock()
	status := room.matchStatus
	room.mu.Unlock()
	if status != MatchStatusLoading {
		t.Fatalf("match status=%q want=%q", status, MatchStatusLoading)
	}
	return store, clock, room, sessions
}

func latestFakeTickerForDuration(t *testing.T, clock *fakeClock, duration time.Duration) *fakeTicker {
	t.Helper()
	clock.mu.Lock()
	defer clock.mu.Unlock()
	for index := len(clock.tickers) - 1; index >= 0; index-- {
		if clock.tickers[index].duration == duration {
			return clock.tickers[index]
		}
	}
	t.Fatalf("missing fake ticker for %s", duration)
	return nil
}

func TestLoadingReadyDeadlineRejectsLateAckBeforeWorkerRuns(t *testing.T) {
	store, clock, room, sessions := loadingReadyDeadlineFixture(t)
	store.markClientReady(room.ID, room.Players[0].ID, sessions[0])

	clock.Advance(30 * time.Second)
	store.markClientReady(room.ID, room.Players[1].ID, sessions[1])

	if current := store.lookupRoom(room.ID); current != nil {
		current.mu.Lock()
		status := current.matchStatus
		current.mu.Unlock()
		t.Fatalf("late ACK retained room in status %q", status)
	}
}

func TestLoadingReadyDeadlineAndAckRaceHasSingleCancellationOwner(t *testing.T) {
	store, clock, room, sessions := loadingReadyDeadlineFixture(t)
	store.markClientReady(room.ID, room.Players[0].ID, sessions[0])
	readyTicker := latestFakeTickerForDuration(t, clock, 30*time.Second)
	clock.Advance(30 * time.Second)

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		store.markClientReady(room.ID, room.Players[1].ID, sessions[1])
	}()
	go func() {
		defer wait.Done()
		<-start
		readyTicker.tick()
	}()
	close(start)
	wait.Wait()
	waitForRoomDeleted(t, store, room.ID)

	waitShutdownCondition(t, "ACK/deadline player ID release", func() bool {
		store.mu.RLock()
		defer store.mu.RUnlock()
		return len(store.playerIDs) == 0
	})
}

func TestLoadingReadyDeadlineRejectsStaleSession(t *testing.T) {
	store, _, room, sessions := loadingReadyDeadlineFixture(t)

	store.markClientReady(room.ID, room.Players[1].ID, sessions[0])
	room.mu.Lock()
	ready := room.readyPlayers[room.Players[1].ID]
	status := room.matchStatus
	room.mu.Unlock()
	if ready || status != MatchStatusLoading {
		t.Fatalf("stale session changed ready state: ready=%v status=%q", ready, status)
	}
}

func TestLoadingReadyDeadlineStopsOnDeleteAndShutdown(t *testing.T) {
	t.Run("delete", func(t *testing.T) {
		store, clock, room, _ := loadingReadyDeadlineFixture(t)
		readyTicker := latestFakeTickerForDuration(t, clock, loadingReadyDeadline)
		if _, deleted := store.deleteRoom(room.ID); !deleted {
			t.Fatal("delete loading room")
		}
		if got := readyTicker.StopCount(); got != 1 {
			t.Fatalf("delete stopped ready ticker %d times, want 1", got)
		}
	})

	t.Run("shutdown", func(t *testing.T) {
		store, clock, _, _ := loadingReadyDeadlineFixture(t)
		readyTicker := latestFakeTickerForDuration(t, clock, loadingReadyDeadline)
		if err := store.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if got := readyTicker.StopCount(); got != 1 {
			t.Fatalf("shutdown stopped ready ticker %d times, want 1", got)
		}
		store.mu.RLock()
		roomCount := len(store.rooms)
		playerIDCount := len(store.playerIDs)
		store.mu.RUnlock()
		if roomCount != 0 || playerIDCount != 0 {
			t.Fatalf("shutdown leaked rooms=%d playerIDs=%d", roomCount, playerIDCount)
		}
	})
}

func TestLoadingReadyDeadlineAckAndShutdownDoNotDeadlock(t *testing.T) {
	store, clock, room, sessions := loadingReadyDeadlineFixture(t)
	store.markClientReady(room.ID, room.Players[0].ID, sessions[0])
	clock.Advance(loadingReadyDeadline)

	start := make(chan struct{})
	readyDone := make(chan struct{})
	shutdownDone := make(chan error, 1)
	go func() {
		<-start
		store.markClientReady(room.ID, room.Players[1].ID, sessions[1])
		close(readyDone)
	}()
	go func() {
		<-start
		shutdownDone <- store.Shutdown(context.Background())
	}()
	close(start)

	select {
	case <-readyDone:
	case <-time.After(time.Second):
		t.Fatal("ready ACK deadlocked with shutdown writer")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown deadlocked with ready ACK")
	}
}

func TestLoadingReadyDeadlineCallbackPanicStillReleasesResources(t *testing.T) {
	panicValue := "ready deadline observer panic"
	observer := &panicOnConnectedClientsObserver{panicValue: panicValue}
	clock := newFakeClock()
	store := newStore(5, clock, StoreConfig{Observer: observer})
	t.Cleanup(func() {
		observer.enabled = false
		store.Close()
	})

	joined := make([]matchmakingJoinResponse, 0, 2)
	for range 2 {
		response, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
		if err != nil {
			t.Fatalf("join human: %v", err)
		}
		joined = append(joined, response)
	}
	room := store.lookupRoom(joined[0].Room.ID)
	sessions := make([]*clientSession, 0, 2)
	for _, response := range joined {
		reservation, err := store.reserveClient(response.Room.ID, response.Player.ID, []string{response.SessionToken})
		if err != nil {
			t.Fatalf("reserve human: %v", err)
		}
		session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
		if !attached {
			t.Fatal("attach human")
		}
		sessions = append(sessions, session)
	}
	readyTicker := latestFakeTickerForDuration(t, clock, loadingReadyDeadline)
	store.markClientReady(room.ID, room.Players[0].ID, sessions[0])
	clock.Advance(loadingReadyDeadline)
	observer.enabled = true

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		store.markClientReady(room.ID, room.Players[1].ID, sessions[1])
	}()
	observer.enabled = false
	if recovered != panicValue {
		t.Fatalf("recovered=%v want=%v", recovered, panicValue)
	}
	if store.lookupRoom(room.ID) != nil {
		t.Fatal("callback panic retained expired Loading room")
	}
	store.mu.RLock()
	playerIDCount := len(store.playerIDs)
	store.mu.RUnlock()
	if playerIDCount != 0 {
		t.Fatalf("callback panic leaked player IDs: %d", playerIDCount)
	}
	if got := readyTicker.StopCount(); got != 1 {
		t.Fatalf("callback panic stopped ready ticker %d times, want 1", got)
	}
}
