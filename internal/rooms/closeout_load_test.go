package rooms

import (
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

// Opt-in accelerated capacity probe. It measures full room processing with
// production bot decisions and simulation, but not real socket writes or 30Hz
// scheduler latency. Run with CRAWLSTARS_CAPACITY_PROBE=1.
func TestCloseoutFiveRoomBotCapacityProbe(t *testing.T) {
	if os.Getenv("CRAWLSTARS_CAPACITY_PROBE") != "1" {
		t.Skip("opt-in capacity probe")
	}
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	var samples []time.Duration
	completed := 0
	for wave := 0; wave < 10; wave++ {
		rooms := make([]*room, 0, 5)
		for i := 0; i < 5; i++ {
			joined, err := store.joinMatchmaking(simulation.GameModeTeam)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.addBots(joined.Room.ID, 5); err != nil {
				t.Fatal(err)
			}
			if _, err := store.startRoom(joined.Room.ID); err != nil {
				t.Fatal(err)
			}
			rooms = append(rooms, store.lookupRoom(joined.Room.ID))
		}
		for tick := 0; tick < 1800; tick++ {
			active := false
			for _, room := range rooms {
				room.mu.Lock()
				ended := room.ending || room.removed
				room.mu.Unlock()
				if ended {
					continue
				}
				active = true
				start := time.Now()
				store.tickRoomState(room)
				samples = append(samples, time.Since(start))
			}
			if !active {
				break
			}
		}
		for _, room := range rooms {
			room.mu.Lock()
			ending := room.ending
			room.mu.Unlock()
			if ending {
				select {
				case <-room.gameEndCleanupDone:
					completed++
				case <-time.After(5 * time.Second):
					t.Fatal("terminal cleanup did not finish")
				}
			} else {
				store.deleteRoom(room.ID)
			}
		}
		if len(store.registeredRooms()) != 0 {
			t.Fatal("room registry retained completed wave")
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	var sum time.Duration
	for _, sample := range samples {
		sum += sample
	}
	t.Logf("5-room waves=10 participants=30/wave human=1+bot=5/room completed=%d/50 samples=%d mean=%s p95=%s p99=%s max=%s", completed, len(samples), sum/time.Duration(len(samples)), samples[len(samples)*95/100], samples[len(samples)*99/100], samples[len(samples)-1])
}
