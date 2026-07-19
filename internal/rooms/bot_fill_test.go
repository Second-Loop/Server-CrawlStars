package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestBotFillAtTenSeconds(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join matchmaking: %v", err)
	}
	room := store.lookupRoom(joined.Room.ID)

	clock.Advance(matchmakingBotFillDelay - time.Nanosecond)
	room.mu.Lock()
	playersBeforeDeadline := len(room.Players)
	statusBeforeDeadline := room.matchStatus
	room.mu.Unlock()
	if playersBeforeDeadline != 1 || statusBeforeDeadline != "" {
		t.Fatalf("fill before 10s: players=%d status=%q", playersBeforeDeadline, statusBeforeDeadline)
	}

	clock.Advance(time.Nanosecond)
	clock.TickTicker(matchmakingBotFillDelay, 0)
	waitForBotFillMatchStatus(t, room, MatchStatusMatched)

	room.mu.Lock()
	players := append([]playerResponse(nil), room.Players...)
	room.mu.Unlock()
	if len(players) != 2 || players[0].IsBot || !players[1].IsBot {
		t.Fatalf("players at 10s=%+v want one human then one bot", players)
	}
}

func TestBotFillUsesEveryModeAssignment(t *testing.T) {
	for _, mode := range []string{
		simulation.GameModeDuel1v1,
		simulation.GameModeSolo,
		simulation.GameModeTeam,
	} {
		selected, err := simulation.StaticGameConfig().SelectMode(mode)
		if err != nil {
			t.Fatalf("select mode %q: %v", mode, err)
		}
		for humanCount := 1; humanCount < selected.MatchPlayerCount(); humanCount++ {
			t.Run(mode+"/humans="+string(rune('0'+humanCount)), func(t *testing.T) {
				clock := newFakeClock()
				store := NewStoreWithClock(5, clock)
				t.Cleanup(store.Close)

				var roomID string
				for index := 0; index < humanCount; index++ {
					joined, joinErr := store.joinMatchmaking(mode)
					if joinErr != nil {
						t.Fatalf("join human %d: %v", index, joinErr)
					}
					if index == 0 {
						roomID = joined.Room.ID
					} else if joined.Room.ID != roomID {
						t.Fatalf("human %d joined room=%q want=%q", index, joined.Room.ID, roomID)
					}
				}

				clock.TickTicker(matchmakingBotFillDelay, 0)
				room := store.lookupRoom(roomID)
				waitForBotFillMatchStatus(t, room, MatchStatusMatched)

				room.mu.Lock()
				players := append([]playerResponse(nil), room.Players...)
				config := room.gameConfig
				room.mu.Unlock()
				if len(players) != config.MatchPlayerCount() {
					t.Fatalf("players=%d want=%d", len(players), config.MatchPlayerCount())
				}
				for index, player := range players {
					wantTeam, wantSlot, ok := config.TeamForPlayerIndex(index)
					if !ok || player.Team != string(wantTeam) || player.Slot != wantSlot {
						t.Fatalf("player[%d]=%+v want team=%q slot=%d", index, player, wantTeam, wantSlot)
					}
					if gotBot := index >= humanCount; player.IsBot != gotBot {
						t.Fatalf("player[%d].IsBot=%t want=%t", index, player.IsBot, gotBot)
					}
				}
			})
		}
	}
}

func TestBotFillHumanFirst(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	reader := newBotFillBarrierReader(0x61)
	store.mu.Lock()
	store.random = reader
	store.mu.Unlock()

	secondResult := make(chan matchmakingJoinResult, 1)
	go func() {
		joined, joinErr := store.joinMatchmaking(simulation.GameModeDuel1v1)
		secondResult <- matchmakingJoinResult{joined: joined, err: joinErr}
	}()
	waitBotFillSignal(t, reader.entered, "human credential entropy")
	clock.TickTicker(matchmakingBotFillDelay, 0)
	reader.release()

	second := waitBotFillJoinResult(t, secondResult)
	if second.err != nil {
		t.Fatalf("join second human: %v", second.err)
	}
	if second.joined.Room.ID != first.Room.ID {
		t.Fatalf("second human room=%q want=%q", second.joined.Room.ID, first.Room.ID)
	}
	room := store.lookupRoom(first.Room.ID)
	room.mu.Lock()
	players := append([]playerResponse(nil), room.Players...)
	room.mu.Unlock()
	if len(players) != 2 || players[0].IsBot || players[1].IsBot {
		t.Fatalf("human-first players=%+v want two humans", players)
	}
}

func TestBotFillTimerFirst(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(2, clock)
	t.Cleanup(store.Close)

	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	reader := newBotFillBarrierReader(0x71)
	store.mu.Lock()
	store.random = reader
	store.mu.Unlock()

	clock.TickTicker(matchmakingBotFillDelay, 0)
	waitBotFillSignal(t, reader.entered, "bot ID entropy")
	lateResult := make(chan matchmakingJoinResult, 1)
	go func() {
		joined, joinErr := store.joinMatchmaking(simulation.GameModeDuel1v1)
		lateResult <- matchmakingJoinResult{joined: joined, err: joinErr}
	}()
	reader.release()

	late := waitBotFillJoinResult(t, lateResult)
	if late.err != nil {
		t.Fatalf("late human join: %v", late.err)
	}
	if late.joined.Room.ID == first.Room.ID {
		t.Fatalf("late human joined bot-filled room %q", first.Room.ID)
	}
	room := store.lookupRoom(first.Room.ID)
	waitForBotFillMatchStatus(t, room, MatchStatusMatched)
	room.mu.Lock()
	players := append([]playerResponse(nil), room.Players...)
	room.mu.Unlock()
	if len(players) != 2 || players[0].IsBot || !players[1].IsBot {
		t.Fatalf("timer-first players=%+v want one human then one bot", players)
	}
}

func TestBotFillRoomCap(t *testing.T) {
	clock := newFakeClock()
	store := newStore(1, clock, StoreConfig{})
	t.Cleanup(store.Close)
	handler := debugHandler(t, store)

	first := joinMatchmakingWithMode(t, handler, simulation.GameModeDuel1v1)
	reader := newBotFillBarrierReader(0x81)
	store.mu.Lock()
	store.random = reader
	store.mu.Unlock()

	clock.TickTicker(matchmakingBotFillDelay, 0)
	waitBotFillSignal(t, reader.entered, "bot ID entropy")
	response := make(chan *httptest.ResponseRecorder, 1)
	lateStarted := make(chan struct{})
	go func() {
		close(lateStarted)
		response <- requestWithBody(handler, http.MethodPost, "/matchmaking/join", `{"gameMode":"duel_1v1"}`)
	}()
	waitBotFillSignal(t, lateStarted, "late handler join start")
	reader.release()

	var recorded *httptest.ResponseRecorder
	select {
	case recorded = <-response:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for capped late join")
	}
	if recorded.Code != http.StatusConflict {
		t.Fatalf("late join status=%d body=%s", recorded.Code, recorded.Body.String())
	}
	assertError(t, recorded, "room_cap_reached")
	room := store.lookupRoom(first.Room.ID)
	waitForBotFillMatchStatus(t, room, MatchStatusMatched)
}

func TestBotFillLogsEntropyFailure(t *testing.T) {
	clock := newFakeClock()
	logs := &lockedLogBuffer{}
	store := newStore(5, clock, StoreConfig{Logger: jsonTestLogger(logs)})
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeSolo)
	if err != nil {
		t.Fatalf("join matchmaking: %v", err)
	}
	reader := &failAfterReadsReader{successfulReads: 1, value: 0x91, err: errors.New("second bot entropy failure")}
	store.mu.Lock()
	beforeIDs := len(store.playerIDs)
	store.random = reader
	store.mu.Unlock()
	room := store.lookupRoom(joined.Room.ID)
	room.mu.Lock()
	beforePlayers := len(room.Players)
	fillTicker := room.botFillTicker
	room.mu.Unlock()

	clock.TickTicker(matchmakingBotFillDelay, 0)
	waitForBotFillLogEvent(t, logs, "bot_fill_failed")

	store.mu.RLock()
	afterIDs := len(store.playerIDs)
	store.mu.RUnlock()
	room.mu.Lock()
	afterPlayers := len(room.Players)
	status := room.matchStatus
	detached := room.botFillTicker == nil && room.botFillStop == nil
	room.mu.Unlock()
	if reader.calls != 2 || afterIDs != beforeIDs || afterPlayers != beforePlayers || status != "" ||
		!detached || fillTicker.(*fakeTicker).StopCount() != 1 {
		t.Fatalf("failed fill calls=%d IDs=%d->%d players=%d->%d status=%q detached=%t stops=%d", reader.calls, beforeIDs, afterIDs, beforePlayers, afterPlayers, status, detached, fillTicker.(*fakeTicker).StopCount())
	}

	matchedLogs := 0
	for _, line := range splitBotFillLogLines(logs.String()) {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if record["event"] != "bot_fill_failed" {
			continue
		}
		matchedLogs++
		if record["level"] != "ERROR" || record["room_id"] != joined.Room.ID {
			t.Fatalf("bot fill log=%v want ERROR event and room_id=%q", record, joined.Room.ID)
		}
	}
	if matchedLogs != 1 {
		t.Fatalf("bot_fill_failed logs=%d want=1 in %s", matchedLogs, logs.String())
	}
}

func TestBotFillFailureLogRunsOutsideCoreLocks(t *testing.T) {
	clock := newFakeClock()
	callbackResult := make(chan string, 1)
	var store *Store
	var room *room
	handler := &callbackLogHandler{handle: func(record slog.Record) {
		if logRecordString(record, "event") != "bot_fill_failed" {
			return
		}
		if !store.mutationMu.TryLock() {
			callbackResult <- "mutationMu held"
			return
		}
		store.mutationMu.Unlock()
		if !store.matchmakingMu.TryLock() {
			callbackResult <- "matchmakingMu held"
			return
		}
		store.matchmakingMu.Unlock()
		if !store.mu.TryLock() {
			callbackResult <- "Store.mu held"
			return
		}
		store.mu.Unlock()
		if !room.mu.TryLock() {
			callbackResult <- "room.mu held"
			return
		}
		room.mu.Unlock()
		callbackResult <- ""
	}}
	store = newStore(5, clock, StoreConfig{Logger: slog.New(handler)})
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeSolo)
	if err != nil {
		t.Fatalf("join matchmaking: %v", err)
	}
	room = store.lookupRoom(joined.Room.ID)
	store.mu.Lock()
	store.random = &failAfterReadsReader{successfulReads: 1, value: 0xa1, err: errors.New("entropy failure")}
	store.mu.Unlock()

	clock.TickTicker(matchmakingBotFillDelay, 0)
	select {
	case lockErr := <-callbackResult:
		if lockErr != "" {
			t.Fatal(lockErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for bot_fill_failed callback")
	}
}

type matchmakingJoinResult struct {
	joined matchmakingJoinResponse
	err    error
}

type botFillBarrierReader struct {
	entered     chan struct{}
	releaseRead chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	calls       int
	base        byte
}

func newBotFillBarrierReader(base byte) *botFillBarrierReader {
	return &botFillBarrierReader{
		entered:     make(chan struct{}),
		releaseRead: make(chan struct{}),
		base:        base,
	}
}

func (reader *botFillBarrierReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	reader.enterOnce.Do(func() {
		close(reader.entered)
		<-reader.releaseRead
	})
	reader.mu.Lock()
	value := reader.base + byte(reader.calls)
	reader.calls++
	reader.mu.Unlock()
	for index := range buffer {
		buffer[index] = value
	}
	return len(buffer), nil
}

func (reader *botFillBarrierReader) release() {
	reader.releaseOnce.Do(func() { close(reader.releaseRead) })
}

func waitForBotFillMatchStatus(t *testing.T, room *room, want MatchStatus) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		room.mu.Lock()
		got := room.matchStatus
		room.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("match status=%q want=%q", got, want)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func waitBotFillSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitBotFillJoinResult(t *testing.T, result <-chan matchmakingJoinResult) matchmakingJoinResult {
	t.Helper()
	select {
	case joined := <-result:
		return joined
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for matchmaking join")
		return matchmakingJoinResult{}
	}
}

func waitForBotFillLogEvent(t *testing.T, logs *lockedLogBuffer, event string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if strings.Contains(logs.String(), `"event":"`+event+`"`) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s log in %s", event, logs.String())
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func splitBotFillLogLines(logs string) []string {
	return strings.Split(strings.TrimSpace(logs), "\n")
}

func TestMatchmakingBotFillTimerStartsOnHumanZeroToOneOnly(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	empty, err := store.createRoom()
	if err != nil {
		t.Fatal(err)
	}
	joined, err := store.joinMatchmaking(store.defaultGameMode())
	if err != nil {
		t.Fatal(err)
	}
	if joined.Room.ID != empty.ID {
		t.Fatalf("joined room=%q want existing empty room=%q", joined.Room.ID, empty.ID)
	}
	if got := clock.TickerCount(matchmakingBotFillDelay); got != 1 {
		t.Fatalf("bot-fill tickers=%d want 1", got)
	}
}

func TestMatchmakingBotFillTimerDoesNotResetAndStopsOnHumanFull(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatal(err)
	}
	room := store.lookupRoom(first.Room.ID)
	room.mu.Lock()
	fillTicker := room.botFillTicker
	room.mu.Unlock()
	if _, err := store.joinMatchmaking(simulation.GameModeDuel1v1); err != nil {
		t.Fatal(err)
	}
	if got := fillTicker.(*fakeTicker).StopCount(); got != 1 {
		t.Fatalf("stop count=%d want 1", got)
	}
}

func TestMatchmakingBotFillTimerStopsExactlyOnceAcrossRoomLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		remove func(*testing.T, *Store, *fakeClock, string)
	}{
		{
			name: "delete",
			remove: func(t *testing.T, store *Store, _ *fakeClock, roomID string) {
				t.Helper()
				if _, deleted := store.deleteRoom(roomID); !deleted {
					t.Fatal("expected room deletion")
				}
			},
		},
		{
			name: "clear",
			remove: func(t *testing.T, store *Store, _ *fakeClock, _ string) {
				t.Helper()
				if cleared := store.clearRooms(); cleared.Deleted != 1 {
					t.Fatalf("cleared=%d want 1", cleared.Deleted)
				}
			},
		},
		{
			name: "ttl cleanup",
			remove: func(t *testing.T, store *Store, clock *fakeClock, _ string) {
				t.Helper()
				clock.Advance(defaultWaitingRoomIdleTTL)
				if got := store.cleanupExpired(clock.Now()); got != 1 {
					t.Fatalf("expired rooms=%d want 1", got)
				}
			},
		},
		{
			name: "debug start",
			remove: func(t *testing.T, store *Store, _ *fakeClock, roomID string) {
				t.Helper()
				if _, err := store.startRoom(roomID); err != nil {
					t.Fatalf("start room: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newFakeClock()
			store := NewStoreWithClock(5, clock)
			t.Cleanup(store.Close)

			joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
			if err != nil {
				t.Fatalf("join matchmaking: %v", err)
			}
			room := store.lookupRoom(joined.Room.ID)
			room.mu.Lock()
			fillTicker := room.botFillTicker
			room.mu.Unlock()

			tt.remove(t, store, clock, joined.Room.ID)
			if got := fillTicker.(*fakeTicker).StopCount(); got != 1 {
				t.Fatalf("stop count=%d want 1", got)
			}
		})
	}
}

func TestMatchmakingBotFillTimerFiresOnceAndDetaches(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatal(err)
	}
	room := store.lookupRoom(joined.Room.ID)
	room.mu.Lock()
	fillTicker := room.botFillTicker
	room.mu.Unlock()

	clock.TickTicker(matchmakingBotFillDelay, 0)
	deadline := time.After(time.Second)
	for {
		room.mu.Lock()
		detached := room.botFillTicker == nil && room.botFillStop == nil
		room.mu.Unlock()
		if detached {
			break
		}
		select {
		case <-deadline:
			t.Fatal("bot-fill worker did not detach timer resources")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := fillTicker.(*fakeTicker).StopCount(); got != 1 {
		t.Fatalf("stop count=%d want 1", got)
	}
}

func TestMatchmakingBotFillIgnoresSameIDRegistryReplacement(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join matchmaking: %v", err)
	}
	original := store.lookupRoom(joined.Room.ID)
	original.mu.Lock()
	fillTicker := original.botFillTicker
	beforePlayers := len(original.Players)
	original.mu.Unlock()

	store.mu.Lock()
	replacement := store.newRoomLocked(original.ID, original.gameConfig)
	store.rooms[original.ID] = replacement
	store.mu.Unlock()

	store.fillMatchmakingBots(original, fillTicker)

	store.mu.RLock()
	registered := store.rooms[original.ID]
	store.mu.RUnlock()
	original.mu.Lock()
	retainedTicker := original.botFillTicker
	afterPlayers := len(original.Players)
	original.mu.Unlock()
	if registered != replacement {
		t.Fatalf("registered room=%p want replacement=%p", registered, replacement)
	}
	if retainedTicker != fillTicker || fillTicker.(*fakeTicker).StopCount() != 0 {
		t.Fatalf("stale timer was detached: retained=%p want=%p stops=%d", retainedTicker, fillTicker, fillTicker.(*fakeTicker).StopCount())
	}
	if afterPlayers != beforePlayers {
		t.Fatalf("Task 1 stale fill appended participants: players=%d want %d", afterPlayers, beforePlayers)
	}

	store.mu.Lock()
	store.rooms[original.ID] = original
	store.mu.Unlock()
}

func TestMatchmakingBotFillTimerArmsWhenFirstHumanJoinsPartialBotRoom(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)
	room := registerWaitingMatchmakingRoom(t, store, simulation.GameModeSolo)

	if _, err := store.addBots(room.ID, 1); err != nil {
		t.Fatalf("add preexisting bot: %v", err)
	}
	joined, err := store.joinMatchmaking(simulation.GameModeSolo)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	if joined.Room.ID != room.ID {
		t.Fatalf("joined room=%q want partial bot room=%q", joined.Room.ID, room.ID)
	}
	room.mu.Lock()
	fillTicker := room.botFillTicker
	room.mu.Unlock()
	if fillTicker == nil || clock.TickerCount(matchmakingBotFillDelay) != 1 {
		t.Fatalf("first human did not arm one timer: ticker=%v count=%d", fillTicker, clock.TickerCount(matchmakingBotFillDelay))
	}
}

func TestPartialAddBotsKeepsOriginalBotFillDeadline(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)
	joined, err := store.joinMatchmaking(simulation.GameModeSolo)
	if err != nil {
		t.Fatalf("join matchmaking: %v", err)
	}
	room := store.lookupRoom(joined.Room.ID)
	room.mu.Lock()
	fillTicker := room.botFillTicker
	room.mu.Unlock()

	if _, err := store.addBots(room.ID, 1); err != nil {
		t.Fatalf("add partial bot: %v", err)
	}
	room.mu.Lock()
	retainedTicker := room.botFillTicker
	room.mu.Unlock()
	if retainedTicker != fillTicker || clock.TickerCount(matchmakingBotFillDelay) != 1 || fillTicker.(*fakeTicker).StopCount() != 0 {
		t.Fatalf("partial add reset deadline: retained=%p want=%p count=%d stops=%d", retainedTicker, fillTicker, clock.TickerCount(matchmakingBotFillDelay), fillTicker.(*fakeTicker).StopCount())
	}
}

func TestFullAddBotsDetachesBotFillDeadline(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)
	joined, err := store.joinMatchmaking(simulation.GameModeSolo)
	if err != nil {
		t.Fatalf("join matchmaking: %v", err)
	}
	room := store.lookupRoom(joined.Room.ID)
	room.mu.Lock()
	fillTicker := room.botFillTicker
	remaining := room.gameConfig.MatchPlayerCount() - len(room.Players)
	room.mu.Unlock()

	if _, err := store.addBots(room.ID, remaining); err != nil {
		t.Fatalf("fill room with bots: %v", err)
	}
	room.mu.Lock()
	detached := room.botFillTicker == nil && room.botFillStop == nil
	room.mu.Unlock()
	if !detached || fillTicker.(*fakeTicker).StopCount() != 1 {
		t.Fatalf("full add did not detach deadline: detached=%t stops=%d", detached, fillTicker.(*fakeTicker).StopCount())
	}
}

func TestMatchmakingCallbackPanicReleasesLocksForNextJoinAndShutdown(t *testing.T) {
	panicValue := errors.New("matchmaking callback panic sentinel")
	for _, callbackKind := range []string{"logger", "observer"} {
		t.Run(callbackKind, func(t *testing.T) {
			config := StoreConfig{}
			switch callbackKind {
			case "logger":
				config.Logger = slog.New(&roomEventPanicLogHandler{event: "room_created", panicValue: panicValue})
			case "observer":
				config.Observer = &activeRoomPanicObserver{count: 1, panicValue: panicValue}
			}
			store := newStore(5, newFakeClock(), config)

			recovered := recoverCall(func() {
				_, _ = store.joinMatchmaking(simulation.GameModeSolo)
			})
			if recovered != panicValue {
				t.Fatalf("recovered=%v want %v", recovered, panicValue)
			}

			joinDone := make(chan error, 1)
			go func() {
				_, err := store.joinMatchmaking(simulation.GameModeSolo)
				joinDone <- err
			}()
			select {
			case err := <-joinDone:
				if err != nil {
					t.Fatalf("post-panic matchmaking: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("post-panic matchmaking blocked on leaked lock")
			}

			shutdownDone := startStoreShutdown(store, context.Background())
			select {
			case err := <-shutdownDone:
				if err != nil {
					t.Fatalf("post-panic shutdown: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("post-panic shutdown blocked on leaked mutation lock")
			}
		})
	}
}

func TestCallbackPanicStopsDetachedBotFillOutsideMutationLock(t *testing.T) {
	t.Run("start logger", func(t *testing.T) {
		panicValue := errors.New("start logger panic sentinel")
		store := newStore(5, newFakeClock(), StoreConfig{
			Logger: slog.New(&roomEventPanicLogHandler{event: "room_started", panicValue: panicValue}),
		})
		joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
		if err != nil {
			t.Fatalf("join matchmaking: %v", err)
		}
		room := store.lookupRoom(joined.Room.ID)
		room.mu.Lock()
		fillTicker := room.botFillTicker
		room.mu.Unlock()

		recovered := recoverCall(func() { _, _ = store.startRoom(room.ID) })
		if recovered != panicValue {
			t.Fatalf("recovered=%v want %v", recovered, panicValue)
		}
		if got := fillTicker.(*fakeTicker).StopCount(); got != 1 {
			t.Fatalf("detached ticker stops=%d want 1", got)
		}
		assertShutdownReturns(t, store)
	})

	t.Run("delete observer", func(t *testing.T) {
		panicValue := errors.New("delete observer panic sentinel")
		store := newStore(5, newFakeClock(), StoreConfig{
			Observer: &activeRoomPanicObserver{count: 0, panicValue: panicValue},
		})
		joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
		if err != nil {
			t.Fatalf("join matchmaking: %v", err)
		}
		room := store.lookupRoom(joined.Room.ID)
		room.mu.Lock()
		fillTicker := room.botFillTicker
		room.mu.Unlock()

		recovered := recoverCall(func() { _, _ = store.deleteRoom(room.ID) })
		if recovered != panicValue {
			t.Fatalf("recovered=%v want %v", recovered, panicValue)
		}
		if got := fillTicker.(*fakeTicker).StopCount(); got != 1 {
			t.Fatalf("detached ticker stops=%d want 1", got)
		}
		assertShutdownReturns(t, store)
	})
}

func registerWaitingMatchmakingRoom(t *testing.T, store *Store, gameMode string) *room {
	t.Helper()
	gameConfig, err := store.gameConfig.SelectMode(gameMode)
	if err != nil {
		t.Fatalf("select game mode: %v", err)
	}
	store.mu.Lock()
	room := store.newRoomLocked("room-partial-bots", gameConfig)
	store.rooms[room.ID] = room
	transition := store.observation.activeRoomsDelta(1)
	store.mu.Unlock()
	store.observation.publish(transition)
	return room
}

func recoverCall(call func()) (recovered any) {
	defer func() { recovered = recover() }()
	call()
	return nil
}

func assertShutdownReturns(t *testing.T, store *Store) {
	t.Helper()
	select {
	case err := <-startStoreShutdown(store, context.Background()):
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked on leaked mutation lock")
	}
}

type roomEventPanicLogHandler struct {
	event      string
	panicValue any
	once       sync.Once
}

func (*roomEventPanicLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *roomEventPanicLogHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message == h.event {
		h.once.Do(func() { panic(h.panicValue) })
	}
	return nil
}

func (h *roomEventPanicLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *roomEventPanicLogHandler) WithGroup(string) slog.Handler { return h }

type activeRoomPanicObserver struct {
	count      int
	panicValue any
	once       sync.Once
}

func (o *activeRoomPanicObserver) SetActiveRooms(count int) {
	if count == o.count {
		o.once.Do(func() { panic(o.panicValue) })
	}
}

func (*activeRoomPanicObserver) SetConnectedClients(int) {}

func (*activeRoomPanicObserver) ObserveTick(time.Duration) {}
