package rooms

import (
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
