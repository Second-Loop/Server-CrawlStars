package rooms

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestAddBotsCreatesOpaqueServerOwnedParticipantsAtomically(t *testing.T) {
	store := NewStore(5)
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join matchmaking: %v", err)
	}
	bots, err := store.addBots(joined.Room.ID, 1)
	if err != nil {
		t.Fatalf("add bot: %v", err)
	}
	if len(bots) != 1 {
		t.Fatalf("expected one bot, got %+v", bots)
	}
	bot := bots[0]
	assertOpaqueID(t, bot.ID, playerIDPrefix, playerIDRandomBytes)
	if !bot.IsBot {
		t.Fatalf("expected server-owned participant IsBot=true, got %+v", bot)
	}
	if joined.Player.IsBot {
		t.Fatalf("expected matchmaking participant IsBot=false, got %+v", joined.Player)
	}

	room := store.lookupRoom(joined.Room.ID)
	if room == nil {
		t.Fatal("expected matched room")
	}
	room.mu.Lock()
	_, botHasSession := room.sessions[bot.ID]
	_, botHasClient := room.clients[bot.ID]
	_, botHasReservation := room.reservations[bot.ID]
	_, humanHasSession := room.sessions[joined.Player.ID]
	playerCount := len(room.Players)
	room.mu.Unlock()
	if botHasSession || botHasClient || botHasReservation {
		t.Fatalf("bot leaked credential state: session=%t client=%t reservation=%t", botHasSession, botHasClient, botHasReservation)
	}
	if !humanHasSession || playerCount != 2 {
		t.Fatalf("expected one human session and two atomic participants, humanSession=%t players=%d", humanHasSession, playerCount)
	}
	if _, err := store.reserveClient(joined.Room.ID, bot.ID, []string{"bot-has-no-token"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("reserve bot: got %v, want ErrUnauthorized", err)
	}
	humanReservation, err := store.reserveClient(joined.Room.ID, joined.Player.ID, []string{joined.SessionToken})
	if err != nil {
		t.Fatalf("reserve existing human credentials: %v", err)
	}
	store.rollbackClientReservation(humanReservation)
}

func TestAddBotsUsesSelectedModeAssignments(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		humanCount int
		botCount   int
	}{
		{name: "solo", mode: simulation.GameModeSolo, humanCount: 1, botCount: 5},
		{name: "team", mode: simulation.GameModeTeam, humanCount: 2, botCount: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			t.Cleanup(store.Close)
			handler := debugHandler(t, store)

			joined := make([]matchmakingJoinResponse, tt.humanCount)
			for index := range joined {
				joined[index] = joinMatchmakingWithMode(t, handler, tt.mode)
				if index > 0 && joined[index].Room.ID != joined[0].Room.ID {
					t.Fatalf("human %d joined room %q, want %q", index, joined[index].Room.ID, joined[0].Room.ID)
				}
			}
			bots, err := store.addBots(joined[0].Room.ID, tt.botCount)
			if err != nil {
				t.Fatalf("add %s bots: %v", tt.mode, err)
			}
			if len(bots) != tt.botCount {
				t.Fatalf("expected %d bots, got %+v", tt.botCount, bots)
			}

			room := store.lookupRoom(joined[0].Room.ID)
			if room == nil {
				t.Fatal("expected bot-filled room")
			}
			room.mu.Lock()
			config := room.gameConfig
			readyPlayers := readyEventPlayers(room.Players, room.gameConfig)
			room.mu.Unlock()

			detailRecorder := request(handler, "GET", "/rooms/"+room.ID)
			if detailRecorder.Code != 200 {
				t.Fatalf("room detail status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
			}
			var detail roomResponse
			decodeResponse(t, detailRecorder, &detail)
			if len(detail.Players) != config.MatchPlayerCount() || len(readyPlayers) != config.MatchPlayerCount() {
				t.Fatalf("expected %d REST/Ready players, got REST=%d Ready=%d", config.MatchPlayerCount(), len(detail.Players), len(readyPlayers))
			}

			for index := range detail.Players {
				wantTeam, wantSlot, ok := config.TeamForPlayerIndex(index)
				if !ok {
					t.Fatalf("missing selected-mode assignment for index %d", index)
				}
				wantBot := index >= tt.humanCount
				restPlayer := detail.Players[index]
				readyPlayer := readyPlayers[index]
				if restPlayer.Team != string(wantTeam) || restPlayer.Slot != wantSlot || restPlayer.IsBot != wantBot {
					t.Fatalf("REST player %d want team=%q slot=%d bot=%t, got %+v", index, wantTeam, wantSlot, wantBot, restPlayer)
				}
				if readyPlayer.ID != restPlayer.ID || readyPlayer.Team != string(wantTeam) || readyPlayer.Slot != wantSlot || readyPlayer.IsBot != wantBot {
					t.Fatalf("Ready player %d want ID=%q team=%q slot=%d bot=%t, got %+v", index, restPlayer.ID, wantTeam, wantSlot, wantBot, readyPlayer)
				}
			}
		})
	}
}

func TestAllBotRoomNeverSatisfiesHumanReadyQuorum(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	room := store.lookupRoom(created.ID)
	if room == nil {
		t.Fatal("expected created room")
	}
	room.mu.Lock()
	capacity := room.gameConfig.MatchPlayerCount()
	room.mu.Unlock()
	if _, err := store.addBots(created.ID, capacity); err != nil {
		t.Fatalf("fill room with bots: %v", err)
	}

	room.mu.Lock()
	attached := room.allMatchClientsAttached()
	ready := room.allMatchPlayersReady()
	status := room.matchStatus
	countdownTicker := room.countdownTicker
	gameplayTicker := room.ticker
	deliveries := room.readyEventDeliveries()
	room.mu.Unlock()
	if attached || ready {
		t.Fatalf("all-bot room satisfied human quorum: attached=%t ready=%t", attached, ready)
	}
	if status != MatchStatusMatched || countdownTicker != nil || gameplayTicker != nil || len(deliveries) != 0 {
		t.Fatalf("all-bot room advanced lifecycle: status=%q countdown=%v gameplay=%v deliveries=%d", status, countdownTicker, gameplayTicker, len(deliveries))
	}
	if got := fakeClock.TickerCount(time.Second); got != 0 {
		t.Fatalf("all-bot room created %d countdown tickers", got)
	}
}

func TestAddBotsIsAllOrNothing(t *testing.T) {
	t.Run("zero count is a no-op", func(t *testing.T) {
		store := NewStore(5)
		t.Cleanup(store.Close)
		bots, err := store.addBots("missing", 0)
		if err != nil || len(bots) != 0 {
			t.Fatalf("add zero bots=%+v err=%v, want empty success", bots, err)
		}
	})

	t.Run("missing room", func(t *testing.T) {
		store := NewStore(5)
		t.Cleanup(store.Close)
		if _, err := store.addBots("missing", 1); !errors.Is(err, ErrRoomNotFound) {
			t.Fatalf("add to missing room: got %v, want ErrRoomNotFound", err)
		}
	})

	for _, state := range []string{"removed", "ending", "matched", "match started", "started"} {
		t.Run(state+" room", func(t *testing.T) {
			store := NewStore(5)
			t.Cleanup(store.Close)
			created, err := store.createRoom()
			if err != nil {
				t.Fatalf("create room: %v", err)
			}
			if _, err := store.addPlayer(created.ID); err != nil {
				t.Fatalf("add human: %v", err)
			}
			room := store.lookupRoom(created.ID)
			room.mu.Lock()
			switch state {
			case "removed":
				room.removed = true
			case "ending":
				room.ending = true
			case "matched":
				room.matchStatus = MatchStatusMatched
				room.readyPlayers = make(map[string]bool)
			case "match started":
				room.matchStatus = MatchStatusStarted
			case "started":
				room.Status = RoomStatusStarted
			}
			room.mu.Unlock()

			_, addErr := store.addBots(created.ID, 1)
			want := ErrRoomFull
			if state == "removed" || state == "ending" {
				want = ErrRoomNotFound
			}
			if !errors.Is(addErr, want) {
				t.Fatalf("add to %s room: got %v, want %v", state, addErr, want)
			}

			room.mu.Lock()
			room.removed = false
			room.ending = false
			room.Status = RoomStatusWaiting
			room.matchStatus = ""
			room.mu.Unlock()
		})
	}

	t.Run("capacity and large count fail before allocation", func(t *testing.T) {
		store := NewStore(5)
		t.Cleanup(store.Close)
		joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
		if err != nil {
			t.Fatalf("join matchmaking: %v", err)
		}
		reader := &failAfterReadsReader{successfulReads: 0, err: errors.New("random must not be read")}
		store.mu.Lock()
		store.random = reader
		store.mu.Unlock()
		for _, count := range []int{2, int(^uint(0) >> 1)} {
			if _, err := store.addBots(joined.Room.ID, count); !errors.Is(err, ErrRoomFull) {
				t.Fatalf("add count %d: got %v, want ErrRoomFull", count, err)
			}
		}
		if reader.calls != 0 {
			t.Fatalf("capacity precheck read random %d times", reader.calls)
		}
	})

	t.Run("random failure rolls back every reserved ID", func(t *testing.T) {
		store := NewStore(5)
		t.Cleanup(store.Close)
		joined, err := store.joinMatchmaking(simulation.GameModeSolo)
		if err != nil {
			t.Fatalf("join solo matchmaking: %v", err)
		}
		readerErr := errors.New("second bot ID entropy failed")
		reader := &failAfterReadsReader{successfulReads: 1, value: 0x71, err: readerErr}
		store.mu.Lock()
		beforeIDs := len(store.playerIDs)
		store.random = reader
		store.mu.Unlock()
		room := store.lookupRoom(joined.Room.ID)
		room.mu.Lock()
		beforePlayers := len(room.Players)
		room.mu.Unlock()

		bots, err := store.addBots(joined.Room.ID, 2)
		if !errors.Is(err, ErrInternal) || len(bots) != 0 {
			t.Fatalf("addBots bots=%+v err=%v, want no bots/ErrInternal", bots, err)
		}
		store.mu.RLock()
		afterIDs := len(store.playerIDs)
		store.mu.RUnlock()
		room.mu.Lock()
		afterPlayers := len(room.Players)
		room.mu.Unlock()
		if reader.calls != 2 || beforeIDs != afterIDs || beforePlayers != afterPlayers {
			t.Fatalf("rollback calls=%d IDs=%d->%d players=%d->%d", reader.calls, beforeIDs, afterIDs, beforePlayers, afterPlayers)
		}
	})
}

func TestAddBotsChecksCapacityBeforeReadingRandom(t *testing.T) {
	store := NewStore(5)
	t.Cleanup(store.Close)
	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join first human: %v", err)
	}
	if _, err := store.joinMatchmaking(simulation.GameModeDuel1v1); err != nil {
		t.Fatalf("join second human: %v", err)
	}
	reader := &failAfterReadsReader{err: errors.New("random must not be read")}
	store.mu.Lock()
	beforeIDs := len(store.playerIDs)
	store.random = reader
	store.mu.Unlock()

	if _, err := store.addBots(first.Room.ID, 1); !errors.Is(err, ErrRoomFull) {
		t.Fatalf("full room add: got %v, want ErrRoomFull", err)
	}
	store.mu.RLock()
	afterIDs := len(store.playerIDs)
	store.mu.RUnlock()
	if reader.calls != 0 || afterIDs != beforeIDs {
		t.Fatalf("full-room precheck read random or changed registry: reads=%d IDs=%d->%d", reader.calls, beforeIDs, afterIDs)
	}
}

func TestAddBotsRejectsRegistryReplacementDuringReservation(t *testing.T) {
	store := NewStore(5)
	t.Cleanup(store.Close)
	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join matchmaking: %v", err)
	}
	original := store.lookupRoom(joined.Room.ID)
	if original == nil {
		t.Fatal("expected original room")
	}
	replacement := store.newRoomLocked(joined.Room.ID, original.gameConfig)
	reader := &registryReplacingReader{
		store:       store,
		roomID:      joined.Room.ID,
		replacement: replacement,
		replaced:    make(chan struct{}),
		value:       0x72,
	}
	store.mu.Lock()
	beforeIDs := len(store.playerIDs)
	store.random = reader
	store.mu.Unlock()
	original.mu.Lock()
	beforePlayers := len(original.Players)
	original.mu.Unlock()

	bots, err := store.addBots(joined.Room.ID, 1)
	if !errors.Is(err, ErrRoomNotFound) || len(bots) != 0 {
		t.Fatalf("addBots bots=%+v err=%v, want no bots/ErrRoomNotFound", bots, err)
	}
	waitShutdownSignal(t, reader.replaced, "registry replacement")
	store.mu.RLock()
	afterIDs := len(store.playerIDs)
	current := store.rooms[joined.Room.ID]
	store.mu.RUnlock()
	original.mu.Lock()
	afterPlayers := len(original.Players)
	original.mu.Unlock()
	if current != replacement || beforeIDs != afterIDs || beforePlayers != afterPlayers {
		t.Fatalf("replacement rollback current=%p want=%p IDs=%d->%d players=%d->%d", current, replacement, beforeIDs, afterIDs, beforePlayers, afterPlayers)
	}

	store.mu.Lock()
	store.rooms[joined.Room.ID] = original
	store.mu.Unlock()
}

func TestAddBotsRollsBackWhenCleanupWinsBeforeAppend(t *testing.T) {
	store := NewStore(5)
	random := newShutdownBarrierReader(0x42)
	t.Cleanup(func() {
		random.release()
		store.Close()
	})

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add human: %v", err)
	}
	room := store.lookupRoom(created.ID)
	if room == nil {
		t.Fatal("expected room")
	}
	store.mu.Lock()
	store.random = random
	store.mu.Unlock()

	type addResult struct {
		bots []playerResponse
		err  error
	}
	added := make(chan addResult, 1)
	go func() {
		bots, addErr := store.addBots(created.ID, 1)
		added <- addResult{bots: bots, err: addErr}
	}()
	waitShutdownSignal(t, random.entered, "addBots random read")

	removedSignal := make(chan struct{})
	cleanupResult := make(chan error, 1)
	go func() {
		var resources roomResources
		room.mu.Lock()
		playerIDs, removed := resources.removeRoomLocked(room)
		close(removedSignal)
		room.mu.Unlock()
		if !removed {
			cleanupResult <- fmt.Errorf("expected cleanup to mark room removed")
			return
		}
		if !store.deleteRoomIfSame(room.ID, room) {
			cleanupResult <- fmt.Errorf("expected cleanup registry delete")
			return
		}
		store.releasePlayerIDs(playerIDs)
		resources.close(defaultRoomDebugDeleteMsg)
		cleanupResult <- nil
	}()
	waitShutdownSignal(t, removedSignal, "cleanup removed mark")
	random.release()

	select {
	case result := <-added:
		if !errors.Is(result.err, ErrRoomNotFound) || len(result.bots) != 0 {
			t.Fatalf("addBots result=%+v err=%v, want no bots/ErrRoomNotFound", result.bots, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for addBots rollback")
	}
	select {
	case cleanupErr := <-cleanupResult:
		if cleanupErr != nil {
			t.Fatal(cleanupErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cleanup")
	}

	store.mu.RLock()
	rooms := len(store.rooms)
	playerIDs := len(store.playerIDs)
	store.mu.RUnlock()
	if rooms != 0 || playerIDs != 0 {
		t.Fatalf("cleanup leaked registry state: rooms=%d playerIDs=%d", rooms, playerIDs)
	}
}

func TestAddBotsCommitThenCleanupReleasesEveryID(t *testing.T) {
	store := NewStore(5)
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	human, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add human: %v", err)
	}

	allowCleanup := make(chan struct{})
	cleanupStarted := make(chan struct{})
	cleanupResult := make(chan clearRoomsResponse, 1)
	var releaseCleanupOnce sync.Once
	releaseCleanup := func() {
		releaseCleanupOnce.Do(func() { close(allowCleanup) })
	}
	t.Cleanup(releaseCleanup)
	go func() {
		close(cleanupStarted)
		<-allowCleanup
		cleanupResult <- store.clearRooms()
	}()
	waitShutdownSignal(t, cleanupStarted, "cleanup goroutine start")

	bots, err := store.addBots(created.ID, 1)
	if err != nil || len(bots) != 1 {
		t.Fatalf("addBots bots=%+v err=%v", bots, err)
	}
	store.mu.RLock()
	_, humanRegistered := store.playerIDs[human.Player.ID]
	_, botRegistered := store.playerIDs[bots[0].ID]
	store.mu.RUnlock()
	if !humanRegistered || !botRegistered {
		t.Fatalf("expected committed IDs, human=%t bot=%t", humanRegistered, botRegistered)
	}

	releaseCleanup()
	select {
	case cleared := <-cleanupResult:
		if cleared.Deleted != 1 {
			t.Fatalf("clearRooms deleted=%d, want 1", cleared.Deleted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for clearRooms")
	}
	store.mu.RLock()
	rooms := len(store.rooms)
	playerIDs := len(store.playerIDs)
	_, humanRegistered = store.playerIDs[human.Player.ID]
	_, botRegistered = store.playerIDs[bots[0].ID]
	store.mu.RUnlock()
	if rooms != 0 || playerIDs != 0 || humanRegistered || botRegistered {
		t.Fatalf("cleanup leaked IDs: rooms=%d playerIDs=%d human=%t bot=%t", rooms, playerIDs, humanRegistered, botRegistered)
	}
}

type failAfterReadsReader struct {
	successfulReads int
	calls           int
	value           byte
	err             error
}

func (reader *failAfterReadsReader) Read(buffer []byte) (int, error) {
	reader.calls++
	if reader.calls > reader.successfulReads {
		return 0, reader.err
	}
	for index := range buffer {
		buffer[index] = reader.value
	}
	return len(buffer), nil
}

type registryReplacingReader struct {
	store       *Store
	roomID      string
	replacement *room
	replaced    chan struct{}
	once        sync.Once
	value       byte
}

func (reader *registryReplacingReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	reader.once.Do(func() {
		// addBots owns Store.mu while randomValue calls this reader.
		reader.store.rooms[reader.roomID] = reader.replacement
		close(reader.replaced)
	})
	for index := range buffer {
		buffer[index] = reader.value
	}
	return len(buffer), nil
}
