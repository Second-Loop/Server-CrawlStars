package rooms

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

func TestReconnectGraceKeepsPlayerAliveUntilExactDeadline(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	started, token := createStartedRoomWithToken(t, store)
	room := store.lookupRoom(started.ID)
	if room == nil {
		t.Fatal("expected started room")
	}
	playerID := started.Players[0].ID
	reservation, err := store.reserveClient(started.ID, playerID, []string{token})
	if err != nil {
		t.Fatalf("reserve player: %v", err)
	}
	session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
	if !attached {
		t.Fatal("expected player session to attach")
	}

	session.closeWithCause(websocket.StatusGoingAway, "transport lost", websocketCloseCauseReadFailure, "read_failed", "")
	room.mu.Lock()
	if len(room.reconnectGraces) != 1 {
		room.mu.Unlock()
		t.Fatalf("pending reconnect graces=%d, want 1", len(room.reconnectGraces))
	}
	room.mu.Unlock()

	clock.Advance(defaultReconnectGrace - time.Nanosecond)
	store.tickRoomState(room)
	if player := roomPlayerState(t, room, playerID); player.IsDead || player.HP <= 0 {
		t.Fatalf("player expired before deadline: %+v", player)
	}

	clock.Advance(time.Nanosecond)
	store.tickRoomState(room)
	player := roomPlayerState(t, room, playerID)
	if player.HP != 0 || !player.IsDead {
		t.Fatalf("player=%+v, want exact-deadline HP=0 and IsDead=true", player)
	}
	room.mu.Lock()
	deferred := len(room.reconnectGraces)
	room.mu.Unlock()
	if deferred != 0 {
		t.Fatalf("pending reconnect graces=%d after expiry, want 0", deferred)
	}
}

func TestReconnectBeforeDeadlinePreservesTickAndCancelsExpiry(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	started, token := createStartedRoomWithToken(t, store)
	room := store.lookupRoom(started.ID)
	if room == nil {
		t.Fatal("expected started room")
	}
	playerID := started.Players[0].ID
	firstReservation, err := store.reserveClient(started.ID, playerID, []string{token})
	if err != nil {
		t.Fatalf("reserve first session: %v", err)
	}
	firstSession, attached := store.attachClientSession(firstReservation, newFakeClientConn(false))
	if !attached {
		t.Fatal("expected first session to attach")
	}
	store.tickRoomState(room)
	room.mu.Lock()
	beforeTick := room.latestSnapshot.Tick
	beforePlayers := append([]simulation.PlayerData(nil), room.lastPlayers...)
	room.mu.Unlock()

	firstSession.closeWithCause(websocket.StatusGoingAway, "transport lost", websocketCloseCausePeerClose, "", "")
	clock.Advance(3 * time.Second)

	secondReservation, err := store.reserveClient(started.ID, playerID, []string{token})
	if err != nil {
		t.Fatalf("reserve reconnect: %v", err)
	}
	secondSession, attached := store.attachClientSession(secondReservation, newFakeClientConn(false))
	if !attached {
		t.Fatal("expected reconnect session to attach")
	}
	if secondSession == firstSession {
		t.Fatal("reconnect must use a new session")
	}
	room.mu.Lock()
	gotTick := room.latestSnapshot.Tick
	gotPlayers := append([]simulation.PlayerData(nil), room.lastPlayers...)
	gotGraces := len(room.reconnectGraces)
	room.mu.Unlock()
	if gotTick != beforeTick {
		t.Fatalf("reconnect tick=%d, want preserved tick %d", gotTick, beforeTick)
	}
	if gotGraces != 0 {
		t.Fatalf("pending reconnect graces=%d after reconnect, want 0", gotGraces)
	}
	if len(gotPlayers) != len(beforePlayers) {
		t.Fatalf("reconnect player count=%d, want %d", len(gotPlayers), len(beforePlayers))
	}
	for index := range beforePlayers {
		if gotPlayers[index] != beforePlayers[index] {
			t.Fatalf("reconnect changed player state at index %d: got=%+v want=%+v", index, gotPlayers[index], beforePlayers[index])
		}
	}

	// A delayed callback from the old generation must not reserve a new grace.
	store.releaseClient(firstReservation, firstSession)
	clock.Advance(defaultReconnectGrace)
	store.tickRoomState(room)
	if player := roomPlayerState(t, room, playerID); player.IsDead {
		t.Fatalf("stale old-session close caused expiry: %+v", player)
	}
}

func TestReconnectableTransportCausesScheduleGrace(t *testing.T) {
	causes := []websocketCloseCause{
		websocketCloseCausePeerClose,
		websocketCloseCauseReadFailure,
		websocketCloseCauseWriteTimeout,
		websocketCloseCauseWriteError,
		websocketCloseCausePingTimeout,
		websocketCloseCausePingError,
		websocketCloseCauseControlOverflow,
	}
	for _, cause := range causes {
		t.Run(string(cause), func(t *testing.T) {
			clock := newFakeClock()
			store := NewStoreWithClock(5, clock)
			t.Cleanup(store.Close)

			started, token := createStartedRoomWithToken(t, store)
			room := store.lookupRoom(started.ID)
			reservation, err := store.reserveClient(started.ID, started.Players[0].ID, []string{token})
			if err != nil {
				t.Fatalf("reserve player: %v", err)
			}
			session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
			if !attached {
				t.Fatal("expected player session to attach")
			}

			session.closeWithCause(websocket.StatusGoingAway, "transport lost", cause, "", "")
			room.mu.Lock()
			pending, ok := room.reconnectGraces[started.Players[0].ID]
			generation := room.connectionGenerations[started.Players[0].ID]
			room.mu.Unlock()
			if !ok {
				t.Fatalf("cause %q did not schedule reconnect grace", cause)
			}
			if pending.generation != generation {
				t.Fatalf("cause %q pending generation=%d, current generation=%d", cause, pending.generation, generation)
			}
			if !pending.expiresAt.Equal(clock.Now().Add(defaultReconnectGrace)) {
				t.Fatalf("cause %q expiry=%s, want %s", cause, pending.expiresAt, clock.Now().Add(defaultReconnectGrace))
			}
		})
	}
}

func TestIntentionalCloseCausesDoNotExpireStartedPlayers(t *testing.T) {
	causes := []websocketCloseCause{
		websocketCloseCauseGameEnd,
		websocketCloseCauseExpiry,
		websocketCloseCauseShutdown,
		websocketCloseCauseDebugDelete,
		websocketCloseCausePrestartCancel,
	}
	for _, cause := range causes {
		t.Run(string(cause), func(t *testing.T) {
			clock := newFakeClock()
			store := NewStoreWithClock(5, clock)
			t.Cleanup(store.Close)

			started, token := createStartedRoomWithToken(t, store)
			room := store.lookupRoom(started.ID)
			playerID := started.Players[0].ID
			reservation, err := store.reserveClient(started.ID, playerID, []string{token})
			if err != nil {
				t.Fatalf("reserve player: %v", err)
			}
			session, attached := store.attachClientSession(reservation, newFakeClientConn(false))
			if !attached {
				t.Fatal("expected player session to attach")
			}

			session.closeWithCause(websocket.StatusNormalClosure, "intentional", cause, "", "")
			clock.Advance(defaultReconnectGrace)
			store.tickRoomState(room)
			if player := roomPlayerState(t, room, playerID); player.IsDead {
				t.Fatalf("intentional cause %q expired player: %+v", cause, player)
			}
			room.mu.Lock()
			deferred := len(room.reconnectGraces)
			room.mu.Unlock()
			if deferred != 0 {
				t.Fatalf("intentional cause %q left pending grace=%d", cause, deferred)
			}
		})
	}
}

func TestReconnectGraceBatchExpiresAllPlayersThroughModeEvaluator(t *testing.T) {
	for _, mode := range []string{simulation.GameModeDuel1v1, simulation.GameModeSolo, simulation.GameModeTeam} {
		t.Run(mode, func(t *testing.T) {
			selected, err := simulation.StaticGameConfig().SelectMode(mode)
			if err != nil {
				t.Fatalf("select mode %q: %v", mode, err)
			}
			playerCount := selected.MatchPlayerCount()
			connected := make([]int, playerCount)
			for index := range connected {
				connected[index] = index
			}
			harness := newModeTickHarness(t, mode, nil, nil, connected...)
			stepper := installBatchRecordingStepper(t, harness.room)

			for _, session := range harness.sessions {
				session.closeWithCause(websocket.StatusGoingAway, "transport lost", websocketCloseCausePeerClose, "", "")
			}
			harness.clock.Advance(defaultReconnectGrace)
			harness.store.tickRoomState(harness.room)

			if len(stepper.eliminations) != 1 {
				t.Fatalf("batch elimination calls=%d, want exactly one", len(stepper.eliminations))
			}
			if len(stepper.eliminations[0]) != playerCount {
				t.Fatalf("batch IDs=%v, want %d players", stepper.eliminations[0], playerCount)
			}
			harness.room.mu.Lock()
			results := make(map[string]gameEndResult, len(harness.room.finalizedGameEndResults))
			for playerID, result := range harness.room.finalizedGameEndResults {
				results[playerID] = result
			}
			harness.room.mu.Unlock()
			if len(results) != playerCount {
				t.Fatalf("finalized results=%v, want %d players", results, playerCount)
			}
			for playerID, result := range results {
				if result != gameEndResultDraw {
					t.Fatalf("player %q result=%q, want simultaneous Draw; all results=%v", playerID, result, results)
				}
			}
		})
	}
}

func TestReconnectAfterFinalizedExpiryIsRejected(t *testing.T) {
	harness := newModeTickHarness(t, simulation.GameModeSolo, nil, nil, 0)
	playerID := harness.playerID(0)
	token := harness.joined[0].SessionToken
	harness.sessions[0].closeWithCause(websocket.StatusGoingAway, "transport lost", websocketCloseCausePeerClose, "", "")
	harness.clock.Advance(defaultReconnectGrace)
	harness.store.tickRoomState(harness.room)

	harness.room.mu.Lock()
	result, finalized := harness.room.finalizedGameEndResults[playerID]
	harness.room.mu.Unlock()
	if !finalized || result != gameEndResultLose {
		t.Fatalf("expired player result=%q finalized=%v, want Lose/finalized", result, finalized)
	}
	if _, err := harness.store.reserveClient(harness.room.ID, playerID, []string{token}); err != ErrPlayerNotFound {
		t.Fatalf("reserve finalized player error=%v, want ErrPlayerNotFound", err)
	}
}

func TestReconnectReservationAndExpiryAtDeadlineAreSerialized(t *testing.T) {
	t.Run("reservation wins before gameplay tick", func(t *testing.T) {
		clock := newFakeClock()
		store := NewStoreWithClock(5, clock)
		t.Cleanup(store.Close)

		started, token := createStartedRoomWithToken(t, store)
		room := store.lookupRoom(started.ID)
		playerID := started.Players[0].ID
		firstReservation, err := store.reserveClient(started.ID, playerID, []string{token})
		if err != nil {
			t.Fatalf("reserve first session: %v", err)
		}
		firstSession, attached := store.attachClientSession(firstReservation, newFakeClientConn(false))
		if !attached {
			t.Fatal("expected first session to attach")
		}
		firstSession.closeWithCause(websocket.StatusGoingAway, "transport lost", websocketCloseCausePeerClose, "", "")
		clock.Advance(defaultReconnectGrace)

		reconnectReservation, err := store.reserveClient(started.ID, playerID, []string{token})
		if err != nil {
			t.Fatalf("reserve reconnect at deadline: %v", err)
		}
		if _, attached := store.attachClientSession(reconnectReservation, newFakeClientConn(false)); !attached {
			t.Fatal("expected reconnect attach to win before gameplay tick")
		}
		store.tickRoomState(room)
		if player := roomPlayerState(t, room, playerID); player.IsDead {
			t.Fatalf("reservation-first deadline caused expiry: %+v", player)
		}
	})

	t.Run("gameplay tick wins before reservation", func(t *testing.T) {
		harness := newModeTickHarness(t, simulation.GameModeSolo, nil, nil, 0)
		playerID := harness.playerID(0)
		harness.sessions[0].closeWithCause(websocket.StatusGoingAway, "transport lost", websocketCloseCausePeerClose, "", "")
		harness.clock.Advance(defaultReconnectGrace)
		harness.store.tickRoomState(harness.room)
		if _, err := harness.store.reserveClient(harness.room.ID, playerID, []string{harness.joined[0].SessionToken}); err != ErrPlayerNotFound {
			t.Fatalf("gameplay-first reserve error=%v, want ErrPlayerNotFound", err)
		}
	})
}

func TestReconnectExpiryKeepsTerminalMessageOrder(t *testing.T) {
	harness := newModeTickHarness(t, simulation.GameModeDuel1v1, nil, nil, 0, 1)
	harness.sessions[0].closeWithCause(websocket.StatusGoingAway, "transport lost", websocketCloseCausePeerClose, "", "")
	harness.clock.Advance(defaultReconnectGrace)
	harness.store.tickRoomState(harness.room)

	var messages []struct {
		Type string `json:"Type"`
	}
	for range 2 {
		select {
		case payload := <-harness.connections[1].writes:
			var message struct {
				Type string `json:"Type"`
			}
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode terminal payload: %v", err)
			}
			messages = append(messages, message)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for terminal snapshot/GameEnd")
		}
	}
	if messages[0].Type != "snapshot" || messages[1].Type != "GameEnd" {
		t.Fatalf("terminal messages=%v, want snapshot then GameEnd", messages)
	}
	deadline := time.Now().Add(time.Second)
	for harness.connections[1].closeCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := harness.connections[1].closeCount.Load(); got != 1 {
		t.Fatalf("terminal close count=%d, want 1", got)
	}
}

func TestReconnectExpiryUpdatesLastPlayersBeforeBotInput(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)
	config, err := simulation.StaticGameConfig().SelectMode(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("select duel mode: %v", err)
	}
	players := []simulation.PlayerData{
		{ID: "human", Team: simulation.TeamRed, Pos: simulation.Vector2{X: -1}},
		{ID: "bot", Team: simulation.TeamBlue, IsBot: true, Pos: simulation.Vector2{X: 1}},
	}
	room := store.newRoomLocked("room-reconnect-bot-input", config)
	room.Status = RoomStatusStarted
	room.matchStatus = MatchStatusStarted
	room.Players = []playerResponse{
		{ID: "human", Team: string(simulation.TeamRed)},
		{ID: "bot", Team: string(simulation.TeamBlue), IsBot: true},
	}
	room.lastPlayers = append([]simulation.PlayerData(nil), players...)
	room.state = &batchRecordingStepper{
		delegate: simulation.NewStateWithConfig(players, simulation.Config{Game: config}),
	}
	room.connectionGenerations["human"] = 1
	room.reconnectGraces["human"] = reconnectGrace{generation: 1, expiresAt: clock.Now()}

	room.mu.Lock()
	stepper := room.state.(*batchRecordingStepper)
	room.mu.Unlock()
	store.tickRoomState(room)

	if len(stepper.eliminations) != 1 || len(stepper.eliminations[0]) != 1 || stepper.eliminations[0][0] != "human" {
		t.Fatalf("elimination batches=%v, want one human batch", stepper.eliminations)
	}
	if len(stepper.inputs) != 1 {
		t.Fatalf("State.Step calls=%d, want 1", len(stepper.inputs))
	}
	if len(stepper.inputs[0]) != 0 {
		t.Fatalf("bot input=%v, want no target after human expiry", stepper.inputs[0])
	}
}

type batchRecordingStepper struct {
	delegate     *simulation.State
	eliminations [][]simulation.PlayerID
	inputs       [][]simulation.InputCommand
}

func (stepper *batchRecordingStepper) EliminatePlayers(ids []simulation.PlayerID) {
	stepper.eliminations = append(stepper.eliminations, append([]simulation.PlayerID(nil), ids...))
	stepper.delegate.EliminatePlayers(ids)
}

func (stepper *batchRecordingStepper) Step(inputs []simulation.InputCommand) simulation.Snapshot {
	stepper.inputs = append(stepper.inputs, append([]simulation.InputCommand(nil), inputs...))
	return stepper.delegate.Step(inputs)
}

func installBatchRecordingStepper(t *testing.T, room *room) *batchRecordingStepper {
	t.Helper()
	room.mu.Lock()
	state, ok := room.state.(*simulation.State)
	if !ok {
		room.mu.Unlock()
		t.Fatalf("room state has type %T, want *simulation.State", room.state)
	}
	stepper := &batchRecordingStepper{delegate: state}
	room.state = stepper
	room.mu.Unlock()
	return stepper
}

func createStartedRoomWithToken(t *testing.T, store *Store) (roomResponse, string) {
	t.Helper()
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	started, err := store.startRoom(created.ID)
	if err != nil {
		t.Fatalf("start room: %v", err)
	}
	return started, issued.SessionToken
}

func roomPlayerState(t *testing.T, room *room, playerID string) simulation.PlayerData {
	t.Helper()
	room.mu.Lock()
	defer room.mu.Unlock()
	for _, player := range room.lastPlayers {
		if player.ID == simulation.PlayerID(playerID) {
			return player
		}
	}
	t.Fatalf("room snapshot missing player %q: %+v", playerID, room.lastPlayers)
	return simulation.PlayerData{}
}
