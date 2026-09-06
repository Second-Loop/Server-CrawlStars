package rooms

import (
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	serverconfig "github.com/Second-Loop/Server-CrawlStars/server-config"
)

// Opt-in accelerated capacity probe. It measures full room processing with
// production bot decisions and simulation, but not real socket writes or 30Hz
// scheduler latency. Run with CRAWLSTARS_CAPACITY_PROBE=1.
func TestCloseoutFiveRoomBotCapacityProbe(t *testing.T) {
	if os.Getenv("CRAWLSTARS_CAPACITY_PROBE") != "1" {
		t.Skip("opt-in capacity probe")
	}
	productionConfig, err := simulation.LoadGameConfig(serverconfig.Reader())
	if err != nil {
		t.Fatalf("load embedded production game config: %v", err)
	}
	if productionConfig.TickRate != 30 || productionConfig.Map.Width != 40 || productionConfig.Map.Height != 40 || productionConfig.Map.Index != 0 || productionConfig.Map.MaxPlayers != 6 {
		t.Fatalf("embedded production config drifted from expected tick/map settings: tickRate=%d map=%+v", productionConfig.TickRate, productionConfig.Map)
	}
	if len(productionConfig.Player.Types) != 3 {
		t.Fatalf("embedded production character catalog size=%d, want 3", len(productionConfig.Player.Types))
	}
	colt, ok := productionConfig.PlayerType(simulation.CharacterTypeColt)
	if !ok || colt.NormalAttack.MaxCharges != 3 || colt.NormalAttack.RechargeTicks != 30 || colt.Skill.CooldownTicks != 390 {
		t.Fatalf("embedded production Colt settings drifted: found=%t config=%+v", ok, colt)
	}
	store := newStore(5, newFakeClock(), StoreConfig{GameConfig: productionConfig})
	t.Cleanup(store.Close)
	if !reflect.DeepEqual(store.gameConfig, productionConfig) {
		t.Fatalf("capacity probe store did not use embedded production game config: store map=%dx%d production map=%dx%d", store.gameConfig.Map.Width, store.gameConfig.Map.Height, productionConfig.Map.Width, productionConfig.Map.Height)
	}
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
			room := store.lookupRoom(joined.Room.ID)
			if room == nil {
				t.Fatalf("wave %d room %d disappeared after start", wave, i)
			}
			room.mu.Lock()
			participants := len(room.Players)
			bots := 0
			for _, player := range room.Players {
				if player.IsBot {
					bots++
				}
			}
			mode := room.gameConfig.SelectedMode.ID
			room.mu.Unlock()
			if participants != 6 || bots != 5 || mode != simulation.GameModeTeam {
				t.Fatalf("wave %d room %d participants=%d bots=%d mode=%q, want human=1 bot=5 team", wave, i, participants, bots, mode)
			}
			rooms = append(rooms, room)
		}
		if registered := len(store.registeredRooms()); registered != 5 {
			t.Fatalf("wave %d registered rooms=%d, want max capacity 5", wave, registered)
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
