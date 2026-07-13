package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

type snapshotMessage struct {
	Type     string              `json:"Type"`
	Snapshot simulation.Snapshot `json:"Snapshot"`
}

type issuedPlayer struct {
	playerResponse
	SessionToken  string
	WebSocketPath string
}

func TestWebSocketTokenRejectsInvalidCredentials(t *testing.T) {
	tests := []struct {
		name string
		path func(issuedPlayer, issuedPlayer) string
	}{
		{
			name: "missing",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return strings.SplitN(first.WebSocketPath, "?", 2)[0]
			},
		},
		{
			name: "empty",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return strings.SplitN(first.WebSocketPath, "?", 2)[0] + "?token="
			},
		},
		{
			name: "wrong",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return strings.SplitN(first.WebSocketPath, "?", 2)[0] + "?token=wrong"
			},
		},
		{
			name: "another player",
			path: func(first issuedPlayer, second issuedPlayer) string {
				return strings.SplitN(first.WebSocketPath, "?", 2)[0] + "?token=" + second.SessionToken
			},
		},
		{
			name: "multiple",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return first.WebSocketPath + "&token=extra"
			},
		},
		{
			name: "malformed query pair",
			path: func(first issuedPlayer, _ issuedPlayer) string {
				return first.WebSocketPath + "&bad=one;two"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStoreWithClock(5, newFakeClock())
			handler := debugHandler(t, store)
			server := httptest.NewServer(handler)
			defer server.Close()
			defer store.Close()

			room := createRoom(t, handler)
			first := issuePlayer(t, handler, room.ID)
			second := issuePlayer(t, handler, room.ID)

			assertWebSocketDialError(t, server.URL, tt.path(first, second), http.StatusUnauthorized, "unauthorized")
		})
	}
}

func TestWebSocketTokenPreservesUnknownIdentityErrors(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	assertWebSocketDialError(t, server.URL, "/rooms/missing/players/player_missing", http.StatusNotFound, "room_not_found")
	assertWebSocketDialError(t, server.URL, "/rooms/"+room.ID+"/players/player_missing", http.StatusNotFound, "player_not_found")
}

func TestWebSocketTokenAllowsCorrectConnectionAndReconnect(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	issued := issuePlayer(t, handler, room.ID)

	first := dialIssuedPlayer(t, server.URL, issued.WebSocketPath)
	waitForAttachedClient(t, store, room.ID, issued.ID)
	_ = first.Close(websocket.StatusNormalClosure, "")
	waitForDetachedClient(t, store, room.ID, issued.ID)

	second := dialIssuedPlayer(t, server.URL, issued.WebSocketPath)
	defer second.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, issued.ID)
}

func TestWebSocketTokenAuthenticationPrecedesDuplicateCheck(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	issued := issuePlayer(t, handler, room.ID)
	pathWithoutToken := strings.SplitN(issued.WebSocketPath, "?", 2)[0]

	first := dialIssuedPlayer(t, server.URL, issued.WebSocketPath)
	defer first.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, issued.ID)

	assertWebSocketDialError(t, server.URL, pathWithoutToken+"?token=wrong", http.StatusUnauthorized, "unauthorized")
	assertWebSocketDialError(t, server.URL, issued.WebSocketPath, http.StatusConflict, "player_already_connected")
}

func TestWebSocketFailedUpgradeRollsBackReservationWithoutCancelingMatch(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	defer store.Close()

	first := joinMatchmaking(t, handler)
	_ = joinMatchmaking(t, handler)

	rec := request(handler, http.MethodGet, first.WebSocketPath)
	if rec.Code == http.StatusSwitchingProtocols {
		t.Fatal("expected a non-WebSocket request to fail the upgrade")
	}

	matched := store.lookupRoom(first.Room.ID)
	roomExists := matched != nil
	clientCount := 0
	reservationCount := 0
	matchStatus := MatchStatus("")
	if matched != nil {
		matched.mu.Lock()
		clientCount = len(matched.clients)
		reservationCount = len(matched.reservations)
		matchStatus = matched.matchStatus
		matched.mu.Unlock()
	}

	if !roomExists || matchStatus != MatchStatusMatched {
		t.Fatal("expected failed upgrade to preserve the matched room")
	}
	if clientCount != 0 || reservationCount != 0 {
		t.Fatal("expected failed upgrade to leave no client or reservation")
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	conn := dialIssuedPlayer(t, server.URL, first.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, first.Room.ID, first.Player.ID)
}

func TestClientReservationCannotAttachAfterRoomRemoval(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	defer store.Close()
	handler := debugHandler(t, store)
	room := createRoom(t, handler)
	issued := issuePlayer(t, handler, room.ID)

	reservation, err := store.reserveClient(room.ID, issued.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	if _, ok := store.deleteRoom(room.ID); !ok {
		t.Fatal("expected room deletion to succeed")
	}
	if store.attachClient(reservation, nil) {
		t.Fatal("expected attachment to fail after room deletion")
	}
	store.rollbackClientReservation(reservation)
}

func TestClientReservationCannotAttachAfterStoreClose(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	room := createRoom(t, handler)
	issued := issuePlayer(t, handler, room.ID)

	reservation, err := store.reserveClient(room.ID, issued.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	store.Close()
	if store.attachClient(reservation, nil) {
		t.Fatal("expected attachment to fail after store close")
	}
	store.rollbackClientReservation(reservation)
}

func TestStoreStaleClientReleasePreservesReplacementRoomConnection(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	staleConn := new(websocket.Conn)
	if !store.attachClient(reservation, staleConn) {
		t.Fatal("expected stale connection to attach before replacement")
	}

	original := reservation.room
	var resources roomResources
	original.mu.Lock()
	_, removed := resources.removeRoomLocked(original)
	original.mu.Unlock()
	if !removed {
		t.Fatal("expected original room to be marked removed")
	}

	currentConn := new(websocket.Conn)
	store.mu.Lock()
	replacement := store.newRoomLocked(created.ID)
	replacement.Players = append(replacement.Players, issued.Player)
	replacement.clients[issued.Player.ID] = currentConn
	store.rooms[created.ID] = replacement
	store.mu.Unlock()

	store.releaseClient(reservation, staleConn)

	if got := store.lookupRoom(created.ID); got != replacement {
		t.Fatal("expected stale release not to delete the replacement room")
	}
	replacement.mu.Lock()
	gotConn, connected := replacement.clients[issued.Player.ID]
	delete(replacement.clients, issued.Player.ID)
	replacement.mu.Unlock()
	store.Close()
	if !connected || gotConn != currentConn {
		t.Fatal("expected stale release not to detach the replacement room connection")
	}
}

func TestStoreStaleClientReleasePreservesCurrentConnection(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	issued, err := store.addPlayer(created.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	reservation, err := store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	staleConn := new(websocket.Conn)
	if !store.attachClient(reservation, staleConn) {
		t.Fatal("expected stale connection to attach")
	}

	currentConn := new(websocket.Conn)
	room := reservation.room
	room.mu.Lock()
	room.clients[issued.Player.ID] = currentConn
	room.mu.Unlock()

	store.releaseClient(reservation, staleConn)

	room.mu.Lock()
	gotConn, connected := room.clients[issued.Player.ID]
	delete(room.clients, issued.Player.ID)
	room.mu.Unlock()
	store.Close()
	if !connected || gotConn != currentConn {
		t.Fatal("expected stale release not to detach the current connection")
	}
}

func TestClientReservationRollbackRestoresDisconnectedAt(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	defer store.Close()
	handler := debugHandler(t, store)
	roomResponse := createRoom(t, handler)
	issued := issuePlayer(t, handler, roomResponse.ID)
	previousDisconnectedAt := fakeClock.Now().Add(-time.Minute)

	room := store.lookupRoom(roomResponse.ID)
	room.mu.Lock()
	room.Status = RoomStatusStarted
	room.disconnectedAt = previousDisconnectedAt
	room.mu.Unlock()

	reservation, err := store.reserveClient(roomResponse.ID, issued.ID, []string{issued.SessionToken})
	if err != nil {
		t.Fatalf("reserve client: %v", err)
	}
	room.mu.Lock()
	reservedDisconnectedAt := room.disconnectedAt
	room.mu.Unlock()
	if !reservedDisconnectedAt.Equal(previousDisconnectedAt) {
		t.Fatal("expected reservation to preserve the disconnected timestamp")
	}
	store.rollbackClientReservation(reservation)

	room.mu.Lock()
	gotDisconnectedAt := room.disconnectedAt
	reservationCount := len(room.reservations)
	room.mu.Unlock()
	if !gotDisconnectedAt.Equal(previousDisconnectedAt) {
		t.Fatal("expected rollback to restore the disconnected timestamp")
	}
	if reservationCount != 0 {
		t.Fatal("expected rollback to remove the reservation")
	}
}

func TestClientReservationRollbackPreservesActivityAcrossOrders(t *testing.T) {
	tests := []struct {
		name  string
		order []int
	}{
		{name: "first then second", order: []int{0, 1}},
		{name: "second then first", order: []int{1, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := newFakeClock()
			store := NewStoreWithClock(5, fakeClock)
			defer store.Close()
			handler := debugHandler(t, store)
			roomResponse := createRoom(t, handler)
			first := issuePlayer(t, handler, roomResponse.ID)
			second := issuePlayer(t, handler, roomResponse.ID)

			room := store.lookupRoom(roomResponse.ID)
			room.mu.Lock()
			originalLastActivityAt := fakeClock.Now()
			room.lastActivityAt = originalLastActivityAt
			room.mu.Unlock()

			fakeClock.Advance(time.Minute)
			firstReservation, err := store.reserveClient(roomResponse.ID, first.ID, []string{first.SessionToken})
			if err != nil {
				t.Fatalf("reserve first client: %v", err)
			}
			fakeClock.Advance(time.Minute)
			secondReservation, err := store.reserveClient(roomResponse.ID, second.ID, []string{second.SessionToken})
			if err != nil {
				t.Fatalf("reserve second client: %v", err)
			}

			room.mu.Lock()
			gotAfterReservations := room.lastActivityAt
			room.mu.Unlock()
			if !gotAfterReservations.Equal(originalLastActivityAt) {
				t.Fatal("expected reservations not to count as room activity")
			}

			reservations := []*clientReservation{firstReservation, secondReservation}
			for _, index := range tt.order {
				store.rollbackClientReservation(reservations[index])
			}

			room.mu.Lock()
			gotLastActivityAt := room.lastActivityAt
			reservationCount := len(room.reservations)
			room.mu.Unlock()
			if !gotLastActivityAt.Equal(originalLastActivityAt) {
				t.Fatal("expected rollback order not to change room activity")
			}
			if reservationCount != 0 {
				t.Fatalf("expected no reservations, got %d", reservationCount)
			}
		})
	}
}

func TestClientReservationAttachAndRollbackSameTickKeepsAttachActivity(t *testing.T) {
	tests := []struct {
		name          string
		attachedIndex int
		rollbackIndex int
	}{
		{name: "attach first rollback second", attachedIndex: 0, rollbackIndex: 1},
		{name: "attach second rollback first", attachedIndex: 1, rollbackIndex: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := newFakeClock()
			store := NewStoreWithClock(5, fakeClock)
			defer store.Close()
			handler := debugHandler(t, store)
			roomResponse := createRoom(t, handler)
			players := []issuedPlayer{
				issuePlayer(t, handler, roomResponse.ID),
				issuePlayer(t, handler, roomResponse.ID),
			}

			room := store.lookupRoom(roomResponse.ID)
			room.mu.Lock()
			room.lastActivityAt = fakeClock.Now().Add(-time.Minute)
			room.mu.Unlock()

			reservations := make([]*clientReservation, len(players))
			for index, player := range players {
				reservation, err := store.reserveClient(roomResponse.ID, player.ID, []string{player.SessionToken})
				if err != nil {
					t.Fatalf("reserve client %d: %v", index, err)
				}
				reservations[index] = reservation
			}
			if !store.attachClient(reservations[tt.attachedIndex], nil) {
				t.Fatal("expected reservation to attach")
			}
			store.rollbackClientReservation(reservations[tt.rollbackIndex])

			room.mu.Lock()
			gotLastActivityAt := room.lastActivityAt
			reservationCount := len(room.reservations)
			room.mu.Unlock()
			if !gotLastActivityAt.Equal(fakeClock.Now()) {
				t.Fatal("expected successful attachment to remain the latest room activity")
			}
			if reservationCount != 0 {
				t.Fatalf("expected no reservations, got %d", reservationCount)
			}
		})
	}
}

func TestWebSocketConnectsIssuedPlayerAndBroadcastsSnapshotsOnTicks(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)

	fakeClock.Tick()
	first := readSnapshotMessage(t, conn)
	if first.Type != "snapshot" {
		t.Fatalf("expected snapshot message, got %q", first.Type)
	}
	if first.Snapshot.Tick != 1 {
		t.Fatalf("expected first snapshot tick 1, got %d", first.Snapshot.Tick)
	}
	if len(first.Snapshot.Players) != 1 {
		t.Fatalf("expected one player in snapshot, got %+v", first.Snapshot.Players)
	}

	fakeClock.Tick()
	second := readSnapshotMessage(t, conn)
	if second.Snapshot.Tick != 2 {
		t.Fatalf("expected second snapshot tick 2, got %d", second.Snapshot.Tick)
	}
	if fakeClock.RequestedDuration() != time.Second/time.Duration(simulation.TickRate) {
		t.Fatalf("expected 30Hz ticker duration, got %s", fakeClock.RequestedDuration())
	}
}

func TestWebSocketWriteTimeoutStaysWithinRealtimeBudget(t *testing.T) {
	tickBudget := time.Second / time.Duration(simulation.TickRate)
	if webSocketWriteTimeout > tickBudget {
		t.Fatalf("expected websocket write timeout %s to stay within tick budget %s", webSocketWriteTimeout, tickBudget)
	}
	if webSocketWriteTimeout > 10*time.Millisecond {
		t.Fatalf("expected websocket write timeout to stay in 10ms latency budget, got %s", webSocketWriteTimeout)
	}
}

func TestWebSocketRejectsUnknownRoomOrPlayer(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)

	assertWebSocketDialError(t, server.URL, "/rooms/missing/players/player_missing", http.StatusNotFound, "room_not_found")
	assertWebSocketDialError(t, server.URL, "/rooms/"+room.ID+"/players/player_missing", http.StatusNotFound, "player_not_found")
}

func TestWebSocketRejectsDuplicateSamePlayerConnection(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)

	first := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer first.Close(websocket.StatusNormalClosure, "")

	assertWebSocketDialError(t, server.URL, player.WebSocketPath, http.StatusConflict, "player_already_connected")
}

func TestWebSocketRouteAcceptsPercentEncodedIDs(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	encodedPath := strings.Replace(player.WebSocketPath, "room_", "room%5F", 1)
	encodedPath = strings.Replace(encodedPath, "player_", "player%5F", 1)

	conn := dialIssuedPlayer(t, server.URL, encodedPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)
}

func TestWebSocketAllowsWaitingRoomConnectionWithoutBroadcasting(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)

	fakeClock.Tick()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected no gameplay snapshot before room start")
	}
}

func TestWebSocketMatchmakingSendsReadyEventWithMapAndSpawnPositions(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	red := joinMatchmaking(t, handler)
	blue := joinMatchmaking(t, handler)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, red.Room.ID, red.Player.ID)
	waitForAttachedClient(t, store, blue.Room.ID, blue.Player.ID)

	redReady := readReadyEventMessage(t, redConn)
	blueReady := readReadyEventMessage(t, blueConn)
	assertMatchingReadyEvents(t, redReady, blueReady)

	if redReady.Type != "Ready" {
		t.Fatalf("expected Ready event type, got %q", redReady.Type)
	}
	if redReady.Map.Width != red.Room.Map.Width || redReady.Map.Height != red.Room.Map.Height {
		t.Fatalf("expected ready map size %dx%d, got %dx%d", red.Room.Map.Width, red.Room.Map.Height, redReady.Map.Width, redReady.Map.Height)
	}
	if len(redReady.Map.Map) != red.Room.Map.Height || len(redReady.Map.Map[0]) != red.Room.Map.Width {
		t.Fatalf("expected ready map tile grid %dx%d, got %+v", red.Room.Map.Width, red.Room.Map.Height, redReady.Map.Map)
	}
	if redReady.Map.Map[0][0] != int(simulation.TileWall) {
		t.Fatalf("expected ready map rows to be number arrays, got %+v", redReady.Map.Map[0])
	}
	if len(redReady.Players) != 2 {
		t.Fatalf("expected two ready players, got %+v", redReady.Players)
	}
	assertReadyPlayerTeamSlot(t, redReady.Players, red.Player.ID, "red", 0)
	assertReadyPlayerTeamSlot(t, redReady.Players, blue.Player.ID, "blue", 0)

	assignments := simulation.PlayerAssignments([]simulation.PlayerID{
		simulation.PlayerID(red.Player.ID),
		simulation.PlayerID(blue.Player.ID),
	}, store.gameConfig)
	assertReadyPlayerSpawn(t, redReady.Players, red.Player.ID, assignments[0].SpawnPosition)
	assertReadyPlayerSpawn(t, redReady.Players, blue.Player.ID, assignments[1].SpawnPosition)
}

func TestWebSocketMatchmakingUsesSnapshotStatusForReadyCountdownAndStart(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	red := joinMatchmaking(t, handler)
	blue := joinMatchmaking(t, handler)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, red.Room.ID, red.Player.ID)
	waitForAttachedClient(t, store, blue.Room.ID, blue.Player.ID)

	redReady := readReadyEventMessage(t, redConn)
	blueReady := readReadyEventMessage(t, blueConn)
	assertMatchingReadyEvents(t, redReady, blueReady)

	detailRec := request(handler, http.MethodGet, "/rooms/"+red.Room.ID)
	var detail roomResponse
	decodeResponse(t, detailRec, &detail)
	if detail.LatestSnapshot.Tick != 0 {
		t.Fatalf("expected no gameplay snapshot before ready, got latest tick %d", detail.LatestSnapshot.Tick)
	}

	writeWSJSON(t, redConn, readyMessage{Type: "ready"})
	writeWSJSON(t, blueConn, readyMessage{Type: "ready"})
	redStarting := readUntilSnapshotStatus(t, redConn, "starting")
	blueStarting := readUntilSnapshotStatus(t, blueConn, "starting")
	assertMatchingMatchSnapshots(t, redStarting, blueStarting)
	if redStarting.Snapshot.Countdown != 5 {
		t.Fatalf("expected starting countdown 5, got %+v", redStarting.Snapshot)
	}

	for i := 0; i < 4; i++ {
		fakeClock.Tick()
	}

	fakeClock.Tick()
	redStarted := readUntilSnapshotStatus(t, redConn, "started")
	blueStarted := readUntilSnapshotStatus(t, blueConn, "started")
	assertMatchingMatchSnapshots(t, redStarted, blueStarted)

	fakeClock.Tick()
	gameplay := readSnapshotMessage(t, redConn)
	if gameplay.Snapshot.Tick != 1 {
		t.Fatalf("expected first gameplay snapshot tick 1 after countdown, got %d", gameplay.Snapshot.Tick)
	}
}

func TestWebSocketCloseBeforeMatchStartCancelsMatchedRoom(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	red := joinMatchmaking(t, handler)
	blue := joinMatchmaking(t, handler)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, red.Room.ID, red.Player.ID)
	waitForAttachedClient(t, store, blue.Room.ID, blue.Player.ID)
	_ = readReadyEventMessage(t, redConn)
	_ = readReadyEventMessage(t, blueConn)

	_ = redConn.Close(websocket.StatusNormalClosure, "")
	waitForRoomDeleted(t, store, red.Room.ID)

	rec := request(handler, http.MethodGet, "/rooms/"+red.Room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected pre-start close to cancel matched room, got status %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func TestWebSocketKeepsSnapshotStreamAfterInvalidInput(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)

	writeText(t, conn, "{not-json")
	invalidInput := readErrorMessage(t, conn)
	if invalidInput.Error.Code != "invalid_input" {
		t.Fatalf("expected invalid_input error, got %+v", invalidInput.Error)
	}

	fakeClock.Tick()
	message := readSnapshotMessage(t, conn)
	if message.Snapshot.Tick != 1 {
		t.Fatalf("expected stream to continue with tick 1, got %d", message.Snapshot.Tick)
	}
}

func TestWebSocketSendsErrorMessageAfterInvalidInputAndKeepsSnapshotStream(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)

	writeText(t, conn, "{not-json")
	errorMessage := readErrorMessage(t, conn)
	if errorMessage.Type != "error" {
		t.Fatalf("expected error message type, got %q", errorMessage.Type)
	}
	if errorMessage.Error.Code != "invalid_input" {
		t.Fatalf("expected invalid_input error, got %+v", errorMessage.Error)
	}

	fakeClock.Tick()
	snapshot := readSnapshotMessage(t, conn)
	if snapshot.Snapshot.Tick != 1 {
		t.Fatalf("expected stream to continue with tick 1, got %d", snapshot.Snapshot.Tick)
	}
}

func TestStoreCleansUpStartedRoomAfterAllPlayersDisconnect(t *testing.T) {
	fakeClock := newFakeClockAt(time.Date(2026, 5, 30, 7, 0, 0, 0, time.UTC))
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	waitForAttachedClient(t, store, room.ID, player.ID)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	waitForDetachedClient(t, store, room.ID, player.ID)

	fakeClock.Advance(5*time.Minute - time.Nanosecond)
	if rec := request(handler, http.MethodGet, "/rooms/"+room.ID); rec.Code != http.StatusOK {
		t.Fatalf("expected disconnected started room before TTL to exist, got status %d", rec.Code)
	}

	fakeClock.Advance(time.Nanosecond)
	fakeClock.TickTicker(janitorInterval, 0)
	waitForRoomDeleted(t, store, room.ID)
	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected all-disconnected started room after TTL to be cleaned up, got status %d", rec.Code)
	}
	assertError(t, rec, "room_not_found")
}

func TestStoreKeepsConnectedRoomPastDisconnectedCleanupTTL(t *testing.T) {
	fakeClock := newFakeClockAt(time.Date(2026, 5, 30, 7, 0, 0, 0, time.UTC))
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)
	sentinel, err := store.createRoom()
	if err != nil {
		t.Fatalf("create expired sweep sentinel: %v", err)
	}
	sentinelRoom := store.lookupRoom(sentinel.ID)
	sentinelRoom.mu.Lock()
	sentinelRoom.lastActivityAt = fakeClock.Now().Add(-defaultWaitingRoomIdleTTL)
	sentinelRoom.mu.Unlock()

	fakeClock.Advance(5 * time.Minute)
	fakeClock.TickTicker(janitorInterval, 0)
	waitForRoomDeleted(t, store, sentinel.ID)
	rec := request(handler, http.MethodGet, "/rooms/"+room.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected connected room to survive cleanup TTL, got status %d", rec.Code)
	}
}

func TestWaitingRoomAcceptsInputButDoesNotBroadcastSnapshot(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)

	writeText(t, conn, `{"MoveDir":{"x":1,"y":0}}`)
	waitForPendingInput(t, store, room.ID, player.ID)

	fakeClock.Tick()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected no gameplay snapshot before room start")
	}
}

func TestWebSocketAppliesValidInputOnNextBroadcastTick(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)

	writeWSJSON(t, conn, inputMessage{MoveDir: simulation.Vector2{X: 1, Y: 0}})
	waitForPendingInput(t, store, room.ID, player.ID)
	fakeClock.Tick()
	message := readSnapshotMessage(t, conn)
	if message.Snapshot.Players[0].Pos.X == 0 {
		t.Fatalf("expected valid input to move player, got %+v", message.Snapshot.Players[0].Pos)
	}
}

func TestWebSocketUsesClientCompatibleMessageFieldNames(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	player := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.ID)

	writeText(t, conn, `{"MoveDir":{"x":1,"y":0},"AttackDir":{"x":0,"y":1},"PressedAttack":true}`)
	waitForPendingInput(t, store, room.ID, player.ID)
	fakeClock.Tick()

	payload := readWebSocketPayload(t, conn)
	text := string(payload)
	for _, want := range []string{
		`"Snapshot"`,
		`"Players"`,
		`"Id":"` + player.ID + `"`,
		`"Pos":{"x":`,
		`"MoveDir":{"x":1`,
		`"AttackDir":{"x":0,"y":1}`,
		`"PressedAttack":true`,
		`"HP":100`,
		`"IsDead":false`,
		`"OwnerId":"` + player.ID + `"`,
		`"Dir":{"x":0,"y":1}`,
		`"IsDestroyed":false`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected websocket payload to contain %s, got %s", want, text)
		}
	}
	if strings.Contains(text, `"moveDir"`) || strings.Contains(text, `"ownerID"`) || strings.Contains(text, `"X"`) {
		t.Fatalf("expected client-compatible field names, got %s", text)
	}

	var message snapshotMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode snapshot message: %v", err)
	}
	if len(message.Snapshot.Projectiles) != 1 {
		t.Fatalf("expected attack input to create one projectile, got %+v", message.Snapshot.Projectiles)
	}
}

func TestWebSocketBroadcastsTwoPlayerMovementHitHPAndDeathSnapshots(t *testing.T) {
	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{GameConfig: fastRechargeGameConfig()})
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	red := issuePlayer(t, handler, room.ID)
	blue := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, red.ID)
	waitForAttachedClient(t, store, room.ID, blue.ID)

	var movement snapshotMessage
	for i := 0; i < 36; i++ {
		writeWSJSON(t, redConn, inputMessage{MoveDir: simulation.Vector2{X: 1, Y: 0}})
		waitForPendingInput(t, store, room.ID, red.ID)
		movement = tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
	}
	redPlayer := findSnapshotPlayer(t, movement.Snapshot, simulation.PlayerID(red.ID))
	bluePlayer := findSnapshotPlayer(t, movement.Snapshot, simulation.PlayerID(blue.ID))
	if redPlayer.Pos.X <= 0 {
		t.Fatalf("expected red movement to be visible in both snapshots, got %+v", redPlayer.Pos)
	}
	if bluePlayer.HP != simulation.DefaultPlayerHP || bluePlayer.IsDead {
		t.Fatalf("expected blue to start alive at full HP, got %+v", bluePlayer)
	}

	expectedHP := simulation.DefaultPlayerHP
	var hit snapshotMessage
	for hitCount := 0; hitCount < 10; hitCount++ {
		writeWSJSON(t, redConn, inputMessage{
			AttackDir:     simulation.Vector2{X: 0, Y: -1},
			PressedAttack: true,
		})
		waitForPendingInput(t, store, room.ID, red.ID)
		hit = tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)

		for i := 0; i < 8; i++ {
			hit = tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
			bluePlayer = findSnapshotPlayer(t, hit.Snapshot, simulation.PlayerID(blue.ID))
			if bluePlayer.HP < expectedHP {
				expectedHP -= simulation.DefaultProjectileDamage
				if bluePlayer.HP != expectedHP {
					t.Fatalf("expected blue HP %f after hit %d, got %+v", expectedHP, hitCount+1, bluePlayer)
				}
				break
			}
		}
		if bluePlayer.HP != expectedHP {
			t.Fatalf("expected hit %d to be observed by both clients, last blue state %+v", hitCount+1, bluePlayer)
		}
	}

	bluePlayer = findSnapshotPlayer(t, hit.Snapshot, simulation.PlayerID(blue.ID))
	if bluePlayer.HP != 0 || !bluePlayer.IsDead {
		t.Fatalf("expected blue death state in both snapshots, got %+v", bluePlayer)
	}
	if len(hit.Snapshot.Projectiles) == 0 {
		t.Fatal("expected hit/death snapshot to include projectile history")
	}
}

func TestWebSocketSendsGameEndWinLoseAndCleansUpRoom(t *testing.T) {
	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{
		Map:        verticalDuelMap(),
		GameConfig: fastRechargeGameConfig(),
	})
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	red := issuePlayer(t, handler, room.ID)
	blue := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, red.ID)
	waitForAttachedClient(t, store, room.ID, blue.ID)

	for hitCount := 0; hitCount < 10; hitCount++ {
		writeWSJSON(t, redConn, inputMessage{
			AttackDir:     simulation.Vector2{X: 0, Y: -1},
			PressedAttack: true,
		})
		waitForPendingInput(t, store, room.ID, red.ID)
		tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
		tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
	}

	assertGameEnd(t, readGameEndMessage(t, redConn), red.ID, "Win")
	assertGameEnd(t, readGameEndMessage(t, blueConn), blue.ID, "Lose")
	waitForRoomDeleted(t, store, room.ID)
}

func TestWebSocketSendsDrawToBothPlayersWhenBothDieOnSameTick(t *testing.T) {
	fakeClock := newFakeClock()
	store := newStore(5, fakeClock, StoreConfig{
		Map:        verticalDuelMap(),
		GameConfig: fastRechargeGameConfig(),
	})
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer store.Close()

	room := createRoom(t, handler)
	red := issuePlayer(t, handler, room.ID)
	blue := issuePlayer(t, handler, room.ID)
	startRoom(t, handler, room.ID)

	redConn := dialIssuedPlayer(t, server.URL, red.WebSocketPath)
	defer redConn.Close(websocket.StatusNormalClosure, "")
	blueConn := dialIssuedPlayer(t, server.URL, blue.WebSocketPath)
	defer blueConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, red.ID)
	waitForAttachedClient(t, store, room.ID, blue.ID)

	for hitCount := 0; hitCount < 10; hitCount++ {
		writeWSJSON(t, redConn, inputMessage{
			AttackDir:     simulation.Vector2{X: 0, Y: -1},
			PressedAttack: true,
		})
		writeWSJSON(t, blueConn, inputMessage{
			AttackDir:     simulation.Vector2{X: 0, Y: 1},
			PressedAttack: true,
		})
		waitForPendingInput(t, store, room.ID, red.ID)
		waitForPendingInput(t, store, room.ID, blue.ID)
		tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
		tickAndReadMatchingSnapshots(t, fakeClock, redConn, blueConn)
	}

	assertGameEnd(t, readGameEndMessage(t, redConn), red.ID, "Draw")
	assertGameEnd(t, readGameEndMessage(t, blueConn), blue.ID, "Draw")
	waitForRoomDeleted(t, store, room.ID)
}

func TestStoreTicksRoomsInParallel(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)

	first := createStartedRoomInStore(t, store)
	second := createStartedRoomInStore(t, store)
	firstRoom := store.lookupRoom(first.ID)
	secondRoom := store.lookupRoom(second.ID)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstRoom.mu.Lock()
	firstStepper := firstRoom.state
	firstRoom.state = testRoomStepper(func(inputs []simulation.InputCommand) simulation.Snapshot {
		close(firstStarted)
		<-releaseFirst
		return firstStepper.Step(inputs)
	})
	firstRoom.mu.Unlock()

	firstDone := make(chan struct{})
	go func() {
		store.tickRoom(first.ID)
		close(firstDone)
	}()
	<-firstStarted

	secondDone := make(chan struct{})
	go func() {
		store.tickRoom(second.ID)
		close(secondDone)
	}()

	secondCompletedWhileFirstBlocked := false
	select {
	case <-secondDone:
		secondCompletedWhileFirstBlocked = true
	case <-time.After(250 * time.Millisecond):
	}
	close(releaseFirst)
	<-firstDone
	if !secondCompletedWhileFirstBlocked {
		t.Fatal("expected room B tick to complete while room A step is blocked")
	}

	secondRoom.mu.Lock()
	secondTick := secondRoom.latestSnapshot.Tick
	secondRoom.mu.Unlock()
	if secondTick != 1 {
		t.Fatalf("expected room B to advance to tick 1, got %d", secondTick)
	}
}

func TestStoreStaleRoomTickPreservesReplacementPlayerID(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	started := createStartedRoomInStore(t, store)
	original := store.lookupRoom(started.ID)
	player := started.Players[0]

	original.mu.Lock()
	original.state = testRoomStepper(func([]simulation.InputCommand) simulation.Snapshot {
		return simulation.Snapshot{Players: []simulation.PlayerData{{
			ID:     simulation.PlayerID(player.ID),
			IsDead: true,
		}}}
	})
	original.mu.Unlock()

	store.mu.Lock()
	replacement := store.newRoomLocked(started.ID)
	replacement.Players = append(replacement.Players, player)
	store.rooms[started.ID] = replacement
	store.mu.Unlock()

	store.tickRoomState(original)
	if got := store.lookupRoom(started.ID); got != replacement {
		t.Fatal("expected stale room tick not to delete replacement")
	}
	store.mu.RLock()
	_, playerIDReserved := store.playerIDs[player.ID]
	store.mu.RUnlock()
	if !playerIDReserved {
		t.Fatal("expected stale room cleanup not to release replacement player ID")
	}
}

func TestStoreConcurrentInputListDeleteAndTick(t *testing.T) {
	store := NewStoreWithClock(64, newFakeClock())
	t.Cleanup(store.Close)

	for range 32 {
		started := createStartedRoomInStore(t, store)
		playerID := started.Players[0].ID
		begin := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(4)
		go func() {
			defer wg.Done()
			<-begin
			store.setInput(started.ID, playerID, inputMessage{MoveDir: simulation.Vector2{X: 1}})
		}()
		go func() {
			defer wg.Done()
			<-begin
			_ = store.listRooms()
		}()
		go func() {
			defer wg.Done()
			<-begin
			store.tickRoom(started.ID)
		}()
		go func() {
			defer wg.Done()
			<-begin
			store.deleteRoom(started.ID)
		}()
		close(begin)
		wg.Wait()

		if got := store.lookupRoom(started.ID); got != nil {
			t.Fatalf("expected concurrently deleted room %q to leave the registry", started.ID)
		}
	}
}

func TestStoreConcurrentReservationAndDeleteRejectsStaleAttach(t *testing.T) {
	store := NewStoreWithClock(64, newFakeClock())
	t.Cleanup(store.Close)
	resourceTickers := make([]*countingTicker, 0, 32)

	for range 32 {
		created, err := store.createRoom()
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		issued, err := store.addPlayer(created.ID)
		if err != nil {
			t.Fatalf("add player: %v", err)
		}
		resourceTicker := newCountingTicker()
		resourceTickers = append(resourceTickers, resourceTicker)
		resourceStop := make(chan struct{})
		room := store.lookupRoom(created.ID)
		room.mu.Lock()
		room.ticker = resourceTicker
		room.stop = resourceStop
		room.mu.Unlock()

		begin := make(chan struct{})
		var wg sync.WaitGroup
		var reservation *clientReservation
		var reserveErr error
		var deleted bool
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-begin
			reservation, reserveErr = store.reserveClient(created.ID, issued.Player.ID, []string{issued.SessionToken})
		}()
		go func() {
			defer wg.Done()
			<-begin
			_, deleted = store.deleteRoom(created.ID)
		}()
		close(begin)
		wg.Wait()

		if reserveErr != nil && !errors.Is(reserveErr, ErrRoomNotFound) {
			t.Fatalf("expected reservation or room-not-found, got %v", reserveErr)
		}
		if !deleted {
			t.Fatal("expected concurrent delete to remove the room")
		}
		if reservation != nil && store.attachClient(reservation, nil) {
			t.Fatal("expected reservation from a deleted room not to attach")
		}
		if got := store.lookupRoom(created.ID); got != nil {
			t.Fatalf("expected deleted room %q to leave the registry", created.ID)
		}
		select {
		case <-resourceStop:
		default:
			t.Fatal("expected deleted room stop channel to close")
		}
	}
	for index, resourceTicker := range resourceTickers {
		if got := resourceTicker.stopCount.Load(); got != 1 {
			t.Fatalf("expected room %d ticker to stop exactly once, got %d", index, got)
		}
	}
}

func TestStoreConcurrentCountdownAndDelete(t *testing.T) {
	fakeClock := newFakeClock()
	store := NewStoreWithClock(5, fakeClock)
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add player: %v", err)
	}

	countdownTicker := fakeClock.NewTicker(time.Second)
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.matchStatus = MatchStatusStarting
	room.countdown = 2
	room.countdownTicker = countdownTicker
	room.countdownStop = make(chan struct{})
	room.mu.Unlock()

	begin := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-begin
		store.tickMatchCountdown(created.ID, countdownTicker)
	}()
	go func() {
		defer wg.Done()
		<-begin
		store.deleteRoom(created.ID)
	}()
	close(begin)
	wg.Wait()

	if got := store.lookupRoom(created.ID); got != nil {
		t.Fatal("expected countdown/delete race to remove the room")
	}
}

func TestStoreCountdownNaturalCompletionStopsTickerOnce(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add player: %v", err)
	}

	countdownTicker := newCountingTicker()
	room := store.lookupRoom(created.ID)
	room.mu.Lock()
	room.matchStatus = MatchStatusStarting
	room.countdown = 1
	room.countdownTicker = countdownTicker
	room.countdownStop = make(chan struct{})
	room.mu.Unlock()

	if completed := store.tickMatchCountdownRoom(room, countdownTicker); !completed {
		t.Fatal("expected final countdown tick to complete")
	}
	if got := countdownTicker.stopCount.Load(); got != 1 {
		t.Fatalf("expected countdown ticker to stop exactly once, got %d", got)
	}
	room.mu.Lock()
	status := room.matchStatus
	room.mu.Unlock()
	if status != MatchStatusStarted {
		t.Fatalf("expected room to start after countdown, got %q", status)
	}
}

func TestFakeClockTicksIndependentTickersWithSameDuration(t *testing.T) {
	fakeClock := newFakeClock()
	first := fakeClock.NewTicker(time.Second)
	second := fakeClock.NewTicker(time.Second)

	fakeClock.TickTicker(time.Second, 0)
	select {
	case <-first.C():
	default:
		t.Fatal("expected first ticker to receive its tick")
	}
	select {
	case <-second.C():
		t.Fatal("expected second ticker not to receive the first ticker's tick")
	default:
	}
}

type testRoomStepper func([]simulation.InputCommand) simulation.Snapshot

func (step testRoomStepper) Step(inputs []simulation.InputCommand) simulation.Snapshot {
	return step(inputs)
}

func createStartedRoomInStore(t *testing.T, store *Store) roomResponse {
	t.Helper()

	created, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := store.addPlayer(created.ID); err != nil {
		t.Fatalf("add player: %v", err)
	}
	started, err := store.startRoom(created.ID)
	if err != nil {
		t.Fatalf("start room: %v", err)
	}
	return started
}

type countingTicker struct {
	ticks     chan time.Time
	stopCount atomic.Int32
}

func newCountingTicker() *countingTicker {
	return &countingTicker{ticks: make(chan time.Time)}
}

func (t *countingTicker) C() <-chan time.Time {
	return t.ticks
}

func (t *countingTicker) Stop() {
	t.stopCount.Add(1)
}

type fakeClock struct {
	mu        sync.Mutex
	tickers   []*fakeTicker
	stopCount int
	now       time.Time
}

type fakeTicker struct {
	clock    *fakeClock
	duration time.Duration
	ticks    chan time.Time
	stopped  bool
}

func newFakeClock() *fakeClock {
	return newFakeClockAt(time.Date(2026, 5, 30, 7, 0, 0, 0, time.UTC))
}

func fastRechargeGameConfig() simulation.GameConfig {
	config := simulation.StaticGameConfig()
	config.Player.Types[0].AttackRechargeTicks = 1
	return config
}

func newFakeClockAt(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTicker(duration time.Duration) ticker {
	c.mu.Lock()
	defer c.mu.Unlock()

	ticker := &fakeTicker{
		clock:    c,
		duration: duration,
		ticks:    make(chan time.Time, 8),
	}
	c.tickers = append(c.tickers, ticker)
	return ticker
}

func (t *fakeTicker) C() <-chan time.Time {
	return t.ticks
}

func (t *fakeTicker) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return
	}
	t.stopped = true
	t.clock.stopCount++
}

func (c *fakeClock) Tick() {
	c.mu.Lock()
	var ticker *fakeTicker
	for index := len(c.tickers) - 1; index >= 0; index-- {
		if !c.tickers[index].stopped {
			ticker = c.tickers[index]
			break
		}
	}
	c.mu.Unlock()
	if ticker != nil {
		ticker.tick()
	}
}

func (c *fakeClock) TickTicker(duration time.Duration, ordinal int) {
	c.mu.Lock()
	var ticker *fakeTicker
	for _, candidate := range c.tickers {
		if candidate.duration != duration {
			continue
		}
		if ordinal == 0 {
			ticker = candidate
			break
		}
		ordinal--
	}
	c.mu.Unlock()
	if ticker != nil {
		ticker.tick()
	}
}

func (t *fakeTicker) tick() {
	t.clock.mu.Lock()
	if t.stopped {
		t.clock.mu.Unlock()
		return
	}
	now := t.clock.now
	t.clock.mu.Unlock()
	t.ticks <- now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func (c *fakeClock) RequestedDuration() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.tickers) == 0 {
		return 0
	}
	return c.tickers[len(c.tickers)-1].duration

}

func (c *fakeClock) TickerCount(duration time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	for _, ticker := range c.tickers {
		if ticker.duration == duration {
			count++
		}
	}
	return count
}

func (c *fakeClock) TotalTickerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tickers)
}

func (c *fakeClock) StopCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopCount
}

func waitForPendingInput(t *testing.T, store *Store, roomID string, playerID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		room := store.lookupRoom(roomID)
		ok := false
		if room != nil {
			room.mu.Lock()
			_, ok = room.pendingInputs[playerID]
			room.mu.Unlock()
		}
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("expected pending input for player %s", playerID)
}

func waitForAttachedClient(t *testing.T, store *Store, roomID string, playerID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		room := store.lookupRoom(roomID)
		var conn *websocket.Conn
		if room != nil {
			room.mu.Lock()
			conn = room.clients[playerID]
			room.mu.Unlock()
		}
		if conn != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("expected attached websocket client for player %s", playerID)
}

func waitForDetachedClient(t *testing.T, store *Store, roomID string, playerID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		room := store.lookupRoom(roomID)
		ok := false
		if room != nil {
			room.mu.Lock()
			_, ok = room.clients[playerID]
			room.mu.Unlock()
		}
		if !ok {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("expected detached websocket client for player %s", playerID)
}

func waitForRoomDeleted(t *testing.T, store *Store, roomID string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.lookupRoom(roomID) == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("expected room %s to be deleted", roomID)
}

func tickAndReadMatchingSnapshots(t *testing.T, fakeClock *fakeClock, first *websocket.Conn, second *websocket.Conn) snapshotMessage {
	t.Helper()

	fakeClock.Tick()
	firstMessage := readSnapshotMessage(t, first)
	secondMessage := readSnapshotMessage(t, second)
	assertMatchingSnapshots(t, firstMessage, secondMessage)
	return firstMessage
}

func assertMatchingSnapshots(t *testing.T, first snapshotMessage, second snapshotMessage) {
	t.Helper()

	firstPayload, err := json.Marshal(first.Snapshot)
	if err != nil {
		t.Fatalf("marshal first snapshot: %v", err)
	}
	secondPayload, err := json.Marshal(second.Snapshot)
	if err != nil {
		t.Fatalf("marshal second snapshot: %v", err)
	}
	if string(firstPayload) != string(secondPayload) {
		t.Fatalf("expected matching snapshots, got first %s and second %s", firstPayload, secondPayload)
	}
}

func assertMatchingMatchSnapshots(t *testing.T, first matchSnapshotMessage, second matchSnapshotMessage) {
	t.Helper()

	firstPayload, err := json.Marshal(first.Snapshot)
	if err != nil {
		t.Fatalf("marshal first match snapshot: %v", err)
	}
	secondPayload, err := json.Marshal(second.Snapshot)
	if err != nil {
		t.Fatalf("marshal second match snapshot: %v", err)
	}
	if string(firstPayload) != string(secondPayload) {
		t.Fatalf("expected matching match snapshots, got first %s and second %s", firstPayload, secondPayload)
	}
}

func assertMatchingReadyEvents(t *testing.T, first readyEventMessage, second readyEventMessage) {
	t.Helper()

	firstPayload, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first ready event: %v", err)
	}
	secondPayload, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second ready event: %v", err)
	}
	if string(firstPayload) != string(secondPayload) {
		t.Fatalf("expected matching ready events, got first %s and second %s", firstPayload, secondPayload)
	}
}

func assertReadyPlayerSpawn(t *testing.T, players []readyEventPlayer, playerID string, position simulation.Vector2) {
	t.Helper()

	for _, player := range players {
		if player.ID != playerID {
			continue
		}
		if player.SpawnPosition != position {
			t.Fatalf("expected player %s spawn %+v, got %+v", playerID, position, player.SpawnPosition)
		}
		return
	}

	t.Fatalf("expected ready event to include player %s in %+v", playerID, players)
}

func assertReadyPlayerTeamSlot(t *testing.T, players []readyEventPlayer, playerID string, team string, slot int) {
	t.Helper()

	for _, player := range players {
		if player.ID != playerID {
			continue
		}
		if player.Team != team || player.Slot != slot {
			t.Fatalf("expected player %s to be %s slot %d, got %+v", playerID, team, slot, player)
		}
		return
	}

	t.Fatalf("expected ready event to include player %s in %+v", playerID, players)
}

func findSnapshotPlayer(t *testing.T, snapshot simulation.Snapshot, playerID simulation.PlayerID) simulation.PlayerData {
	t.Helper()

	for _, player := range snapshot.Players {
		if player.ID == playerID {
			return player
		}
	}
	t.Fatalf("expected snapshot to include player %s", playerID)
	return simulation.PlayerData{}
}

func createPlayer(t *testing.T, handler http.Handler, roomID string) playerResponse {
	t.Helper()
	return issuePlayer(t, handler, roomID).playerResponse
}

func issuePlayer(t *testing.T, handler http.Handler, roomID string) issuedPlayer {
	t.Helper()

	rec := request(handler, http.MethodPost, "/rooms/"+roomID+"/players")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected create player status 201, got %d", rec.Code)
	}
	var issued playerSessionResponse
	decodeResponse(t, rec, &issued)
	return issuedPlayer{
		playerResponse: issued.Player,
		SessionToken:   issued.SessionToken,
		WebSocketPath:  issued.WebSocketPath,
	}
}

func dialIssuedPlayer(t *testing.T, serverURL string, webSocketPath string) *websocket.Conn {
	t.Helper()

	conn, _, err := websocket.Dial(context.Background(), websocketURLForPath(serverURL, webSocketPath), nil)
	if err != nil {
		t.Fatal("dial issued websocket connection failed")
	}
	return conn
}

func assertWebSocketDialError(t *testing.T, serverURL string, webSocketPath string, status int, code string) {
	t.Helper()

	conn, resp, err := websocket.Dial(context.Background(), websocketURLForPath(serverURL, webSocketPath), nil)
	if err == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatalf("expected websocket dial to fail with status %d", status)
	}
	assertWebSocketErrorResponse(t, resp, status, code)
}

func websocketURLForPath(serverURL string, webSocketPath string) string {
	return "ws" + serverURL[len("http"):] + webSocketPath
}

func startRoom(t *testing.T, handler http.Handler, roomID string) {
	t.Helper()

	rec := request(handler, http.MethodPost, "/rooms/"+roomID+"/start")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected start room status 200, got %d", rec.Code)
	}
}

func writeText(t *testing.T, conn *websocket.Conn, text string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(text)); err != nil {
		t.Fatalf("write websocket text: %v", err)
	}
}

func writeWSJSON(t *testing.T, conn *websocket.Conn, message any) {
	t.Helper()

	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal websocket message: %v", err)
	}
	writeText(t, conn, string(payload))
}

func readSnapshotMessage(t *testing.T, conn *websocket.Conn) snapshotMessage {
	t.Helper()

	payload := readWebSocketPayload(t, conn)

	var message snapshotMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode snapshot message: %v", err)
	}
	return message
}

func readErrorMessage(t *testing.T, conn *websocket.Conn) errorMessage {
	t.Helper()

	payload := readWebSocketPayload(t, conn)

	var message errorMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode error message: %v", err)
	}
	return message
}

func readReadyEventMessage(t *testing.T, conn *websocket.Conn) readyEventMessage {
	t.Helper()

	payload := readWebSocketPayload(t, conn)

	var message readyEventMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode ready event message: %v", err)
	}
	return message
}

func readGameEndMessage(t *testing.T, conn *websocket.Conn) gameEndMessage {
	t.Helper()

	payload := readWebSocketPayload(t, conn)

	var message gameEndMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode game end message: %v", err)
	}
	return message
}

func readUntilSnapshotStatus(t *testing.T, conn *websocket.Conn, status string) matchSnapshotMessage {
	t.Helper()

	for i := 0; i < 4; i++ {
		payload := readWebSocketPayload(t, conn)
		var message matchSnapshotMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatalf("decode match snapshot message: %v", err)
		}
		if message.Type != "snapshot" {
			t.Fatalf("expected snapshot message while waiting for status %q, got %q", status, message.Type)
		}
		if message.Snapshot.Status == status {
			return message
		}
	}

	t.Fatalf("expected snapshot status %q", status)
	return matchSnapshotMessage{}
}

func readWebSocketPayload(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	return payload
}

type readyMessage struct {
	Type string `json:"Type"`
}

type matchSnapshotMessage struct {
	Type     string `json:"Type"`
	Snapshot struct {
		Status      string                      `json:"status"`
		Countdown   int                         `json:"countdown,omitempty"`
		Tick        simulation.Tick             `json:"Tick"`
		Players     []simulation.PlayerData     `json:"Players"`
		Projectiles []simulation.ProjectileData `json:"Projectiles"`
	} `json:"Snapshot"`
}

func assertGameEnd(t *testing.T, message gameEndMessage, playerID string, result string) {
	t.Helper()

	if message.Type != "GameEnd" {
		t.Fatalf("expected GameEnd message type, got %+v", message)
	}
	if message.PlayerID != playerID {
		t.Fatalf("expected GameEnd player %s, got %+v", playerID, message)
	}
	if message.Result != result {
		t.Fatalf("expected GameEnd result %s, got %+v", result, message)
	}
}

func verticalDuelMap() simulation.MapData {
	return simulation.MapData{
		Width:      5,
		Height:     5,
		Index:      0,
		MaxPlayers: 2,
		TileSize:   simulation.TileSize,
		Map: [][]simulation.TileType{
			{simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall},
			{simulation.TileWall, simulation.TileGround, simulation.TileSpawnPoint, simulation.TileGround, simulation.TileWall},
			{simulation.TileWall, simulation.TileGround, simulation.TileSpawnPoint, simulation.TileGround, simulation.TileWall},
			{simulation.TileWall, simulation.TileGround, simulation.TileGround, simulation.TileGround, simulation.TileWall},
			{simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall, simulation.TileWall},
		},
	}
}

func assertWebSocketErrorResponse(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()

	if resp == nil {
		t.Fatalf("expected websocket error response with status %d", status)
	}
	defer resp.Body.Close()

	if resp.StatusCode != status {
		t.Fatalf("expected websocket response status %d, got %d", status, resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected websocket application/json content type, got %q", got)
	}
	var body errorResponse
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read websocket error response: %v", err)
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode websocket error response: %v", err)
	}
	if body.Error.Code != code {
		t.Fatalf("expected websocket error code %q, got %+v", code, body.Error)
	}
	wantMessage := map[string]string{
		"player_already_connected": "player already connected",
		"player_not_found":         "player not found",
		"room_not_found":           "room not found",
		"unauthorized":             "unauthorized",
	}[code]
	if body.Error.Message != wantMessage {
		t.Fatalf("expected websocket error message %q, got %+v", wantMessage, body.Error)
	}
}
