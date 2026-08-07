package rooms

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestMatchedRoomAttachDeadlineCancelsWholeRoom(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	second, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join second human: %v", err)
	}
	if first.Room.ID != second.Room.ID {
		t.Fatalf("matched rooms differ: %q != %q", first.Room.ID, second.Room.ID)
	}

	room := store.lookupRoom(first.Room.ID)
	room.mu.Lock()
	deadlineTicker := room.matchAttachTicker
	deadlineAt := room.matchAttachDeadlineAt
	status := room.matchStatus
	room.mu.Unlock()
	if status != MatchStatusMatched || deadlineTicker == nil {
		t.Fatalf("matched deadline not armed: status=%q ticker=%v", status, deadlineTicker)
	}
	if got, want := deadlineAt.Sub(clock.Now()), matchedAttachDeadline; got != want {
		t.Fatalf("deadline distance=%s want=%s", got, want)
	}

	reservation, err := store.reserveClient(first.Room.ID, first.Player.ID, []string{first.SessionToken})
	if err != nil {
		t.Fatalf("reserve first client: %v", err)
	}
	firstSession, attached := store.attachClientSession(reservation, newFakeClientConn(false))
	if !attached {
		t.Fatal("attach first client")
	}

	clock.Advance(matchedAttachDeadline - time.Nanosecond)
	store.expireMatchedAttachDeadline(room, deadlineTicker)
	if store.lookupRoom(first.Room.ID) == nil {
		t.Fatal("room expired before matched attach deadline")
	}

	clock.Advance(time.Nanosecond)
	store.expireMatchedAttachDeadline(room, deadlineTicker)
	if store.lookupRoom(first.Room.ID) != nil {
		t.Fatal("matched room remained after attach deadline")
	}
	select {
	case <-firstSession.done:
	case <-time.After(time.Second):
		t.Fatal("attached client was not closed with cancelled room")
	}
	store.mu.RLock()
	remainingPlayerIDs := len(store.playerIDs)
	store.mu.RUnlock()
	if remainingPlayerIDs != 0 {
		t.Fatalf("player ID leak after deadline cancellation: %d", remainingPlayerIDs)
	}
}

func TestMatchmakingAttachLifecycleLogsBoundedTransitions(t *testing.T) {
	clock := newFakeClock()
	logs := &lockedLogBuffer{}
	store := newStore(5, clock, StoreConfig{Logger: jsonTestLogger(logs)})
	t.Cleanup(store.Close)

	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	if _, err := store.joinMatchmaking(simulation.GameModeDuel1v1); err != nil {
		t.Fatalf("join second human: %v", err)
	}
	room := store.lookupRoom(first.Room.ID)
	room.mu.Lock()
	deadlineTicker := room.matchAttachTicker
	room.mu.Unlock()
	clock.Advance(matchedAttachDeadline)
	store.expireMatchedAttachDeadline(room, deadlineTicker)

	want := map[string]int{
		"join_committed/human_join":                2,
		"matched/human_join":                       1,
		"waiting_for_attach/attach_deadline_armed": 1,
		"cancelled/attach_deadline_expired":        1,
	}
	got := make(map[string]int)
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log %q: %v", line, err)
		}
		if record["event"] != "matchmaking_transition" {
			continue
		}
		key := record["state"].(string) + "/" + record["cause"].(string)
		got[key]++
		allowed := map[string]bool{
			"time": true, "level": true, "msg": true, "event": true,
			"roomID": true, "state": true, "cause": true,
		}
		for field := range record {
			if !allowed[field] {
				t.Fatalf("unexpected matchmaking log field %q in %v", field, record)
			}
		}
	}
	for transition, count := range want {
		if got[transition] != count {
			t.Fatalf("transition %s count=%d want=%d; all=%v", transition, got[transition], count, got)
		}
	}
}

func TestLostJoinResponseRecoversWithFreshIdentityAfterAttachDeadline(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	lost, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join with lost response: %v", err)
	}
	retry, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("retry join: %v", err)
	}
	if retry.Room.ID != lost.Room.ID || retry.Player.ID == lost.Player.ID {
		t.Fatalf("lost-response reproduction mismatch: lost=%+v retry=%+v", lost, retry)
	}

	room := store.lookupRoom(lost.Room.ID)
	room.mu.Lock()
	deadlineTicker := room.matchAttachTicker
	room.mu.Unlock()
	clock.Advance(matchedAttachDeadline)
	store.expireMatchedAttachDeadline(room, deadlineTicker)

	recovered, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("fresh join after cancellation: %v", err)
	}
	if recovered.Room.ID == lost.Room.ID || recovered.Player.ID == lost.Player.ID || recovered.Player.ID == retry.Player.ID {
		t.Fatalf("recovery reused cancelled identity: lost=%+v retry=%+v recovered=%+v", lost, retry, recovered)
	}
	if store.lookupRoom(recovered.Room.ID) == nil {
		t.Fatal("fresh recovery room was not committed")
	}
}

func TestMatchedAttachDeadlineRejectsReservationAtExactDeadline(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	second, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join second human: %v", err)
	}
	room := store.lookupRoom(first.Room.ID)
	room.mu.Lock()
	deadlineTicker := room.matchAttachTicker
	room.mu.Unlock()

	clock.Advance(matchedAttachDeadline)
	if _, err := store.reserveClient(second.Room.ID, second.Player.ID, []string{second.SessionToken}); err != ErrRoomNotFound {
		t.Fatalf("reserve at exact deadline error=%v, want ErrRoomNotFound", err)
	}
	store.expireMatchedAttachDeadline(room, deadlineTicker)
}

func TestMatchedAttachDeadlineTickerCallbackCancelsRoomAndStopsOnce(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	if _, err := store.joinMatchmaking(simulation.GameModeDuel1v1); err != nil {
		t.Fatalf("join second human: %v", err)
	}
	room := store.lookupRoom(joined.Room.ID)
	room.mu.Lock()
	deadlineTicker := room.matchAttachTicker.(*fakeTicker)
	room.mu.Unlock()

	clock.Advance(matchedAttachDeadline)
	deadlineTicker.tick()
	waitForRoomDeleted(t, store, joined.Room.ID)
	stopDeadline := time.Now().Add(time.Second)
	for deadlineTicker.StopCount() == 0 && time.Now().Before(stopDeadline) {
		time.Sleep(time.Millisecond)
	}
	if got := deadlineTicker.StopCount(); got != 1 {
		t.Fatalf("deadline ticker stops=%d want=1", got)
	}
}

func TestMatchedAttachAndDeadlineRaceHasSingleOwner(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	second, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join second human: %v", err)
	}
	room := store.lookupRoom(first.Room.ID)

	firstReservation, err := store.reserveClient(first.Room.ID, first.Player.ID, []string{first.SessionToken})
	if err != nil {
		t.Fatalf("reserve first client: %v", err)
	}
	firstSession, attached := store.attachClientSession(firstReservation, newFakeClientConn(false))
	if !attached {
		t.Fatal("attach first client")
	}
	secondReservation, err := store.reserveClient(second.Room.ID, second.Player.ID, []string{second.SessionToken})
	if err != nil {
		t.Fatalf("reserve second client: %v", err)
	}

	room.mu.Lock()
	deadlineTicker := room.matchAttachTicker
	room.mu.Unlock()
	clock.Advance(matchedAttachDeadline)

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var secondAttached bool
	go func() {
		defer wait.Done()
		<-start
		_, secondAttached = store.attachClientSession(secondReservation, newFakeClientConn(false))
	}()
	go func() {
		defer wait.Done()
		<-start
		store.expireMatchedAttachDeadline(room, deadlineTicker)
	}()
	close(start)
	wait.Wait()

	current := store.lookupRoom(first.Room.ID)
	store.mu.RLock()
	remainingPlayerIDs := len(store.playerIDs)
	store.mu.RUnlock()
	if secondAttached {
		t.Fatal("attach succeeded at the strict 30-second deadline")
	}

	if current != nil || remainingPlayerIDs != 0 {
		t.Fatalf("deadline winner leaked state: room=%v playerIDs=%d", current, remainingPlayerIDs)
	}
	select {
	case <-firstSession.done:
	case <-time.After(time.Second):
		t.Fatal("deadline winner did not close already attached client")
	}
}

func TestAllHumanAttachCancelsMatchedDeadline(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	second, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join second human: %v", err)
	}
	room := store.lookupRoom(first.Room.ID)

	for _, joined := range []matchmakingJoinResponse{first, second} {
		reservation, reserveErr := store.reserveClient(joined.Room.ID, joined.Player.ID, []string{joined.SessionToken})
		if reserveErr != nil {
			t.Fatalf("reserve %s: %v", joined.Player.ID, reserveErr)
		}
		if _, attached := store.attachClientSession(reservation, newFakeClientConn(false)); !attached {
			t.Fatalf("attach %s", joined.Player.ID)
		}
	}

	room.mu.Lock()
	status := room.matchStatus
	deadlineTicker := room.matchAttachTicker
	deadlineStop := room.matchAttachStop
	deadlineAt := room.matchAttachDeadlineAt
	room.mu.Unlock()
	if status != MatchStatusLoading {
		t.Fatalf("match status=%q want=%q", status, MatchStatusLoading)
	}
	if deadlineTicker != nil || deadlineStop != nil || !deadlineAt.IsZero() {
		t.Fatalf("attach deadline retained after all humans attached: ticker=%v stop=%v at=%v", deadlineTicker, deadlineStop, deadlineAt)
	}

	clock.Advance(matchedAttachDeadline)
	store.expireMatchedAttachDeadline(room, deadlineTicker)
	if store.lookupRoom(first.Room.ID) != room {
		t.Fatal("stale attach deadline removed loading room")
	}
}

func TestBotFillWithAttachedHumanSkipsWaitingForAttach(t *testing.T) {
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
	if _, attached := store.attachClientSession(reservation, newFakeClientConn(false)); !attached {
		t.Fatal("attach human")
	}

	room.mu.Lock()
	fillTicker := room.botFillTicker
	room.mu.Unlock()
	store.fillMatchmakingBots(room, fillTicker)

	room.mu.Lock()
	status := room.matchStatus
	players := append([]playerResponse(nil), room.Players...)
	deadlineTicker := room.matchAttachTicker
	deadlineStop := room.matchAttachStop
	room.mu.Unlock()
	if status != MatchStatusLoading || len(players) != 2 || !players[1].IsBot {
		t.Fatalf("bot-fill attach state: status=%q players=%+v", status, players)
	}
	if deadlineTicker != nil || deadlineStop != nil {
		t.Fatalf("bot-fill retained matched attach deadline: ticker=%v stop=%v", deadlineTicker, deadlineStop)
	}
}

func TestAllBotMatchDoesNotArmHumanAttachDeadline(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addBots(created.ID, store.matchCapacity()); err != nil {
		t.Fatalf("fill all-bot room: %v", err)
	}
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	status := room.matchStatus
	deadlineTicker := room.matchAttachTicker
	deadlineStop := room.matchAttachStop
	room.mu.Unlock()
	if status != MatchStatusMatched {
		t.Fatalf("all-bot match status=%q want=%q", status, MatchStatusMatched)
	}
	if deadlineTicker != nil || deadlineStop != nil {
		t.Fatalf("all-bot room armed human attach deadline: ticker=%v stop=%v", deadlineTicker, deadlineStop)
	}
}

func TestDebugStartDetachesMatchedAttachDeadline(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add human: %v", err)
	}
	if _, err := store.addBots(created.ID, 1); err != nil {
		t.Fatalf("add bot: %v", err)
	}
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	deadlineTicker := room.matchAttachTicker.(*fakeTicker)
	room.mu.Unlock()

	if _, err := store.startRoom(created.ID); err != nil {
		t.Fatalf("debug start: %v", err)
	}
	room.mu.Lock()
	retainedTicker := room.matchAttachTicker
	retainedStop := room.matchAttachStop
	room.mu.Unlock()
	if retainedTicker != nil || retainedStop != nil || deadlineTicker.StopCount() != 1 {
		t.Fatalf("debug start retained deadline: ticker=%v stop=%v stops=%d", retainedTicker, retainedStop, deadlineTicker.StopCount())
	}
}
