package rooms

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

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
