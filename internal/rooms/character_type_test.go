package rooms

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

func TestDecodeMatchmakingJoinRequestPreservesCharacterTypePresence(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantRaw string
		missing bool
	}{
		{name: "missing", body: `{"gameMode":"duel_1v1"}`, missing: true},
		{name: "null", body: `{"characterType":null}`, wantRaw: "null"},
		{name: "zero", body: `{"characterType":0}`, wantRaw: "0"},
		{name: "without game mode", body: `{"characterType":1}`, wantRaw: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := decodeMatchmakingJoinRequest(strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if tt.missing && request.CharacterType != nil {
				t.Fatalf("missing characterType = %s, want nil", request.CharacterType)
			}
			if !tt.missing && string(request.CharacterType) != tt.wantRaw {
				t.Fatalf("raw characterType = %q, want %q", request.CharacterType, tt.wantRaw)
			}
		})
	}
}

func TestCharacterTypeProjectsAcrossRESTResponses(t *testing.T) {
	store := NewStore(5)
	handler := debugHandler(t, store)

	joinRecorder := requestWithBody(handler, http.MethodPost, "/matchmaking/join", `{"characterType":1}`)
	if joinRecorder.Code != http.StatusCreated {
		t.Fatalf("join status = %d, body=%s", joinRecorder.Code, joinRecorder.Body.String())
	}
	var joinObject map[string]json.RawMessage
	decodeResponse(t, joinRecorder, &joinObject)
	assertCharacterTypeJSONKey(t, joinObject["player"], "characterType", "CharacterType", simulation.CharacterTypeColt)
	var joinRoom map[string]json.RawMessage
	if err := json.Unmarshal(joinObject["room"], &joinRoom); err != nil {
		t.Fatalf("decode join room: %v", err)
	}
	assertPlayersCharacterTypeJSON(t, joinRoom["players"], "characterType", "CharacterType", []simulation.CharacterType{simulation.CharacterTypeColt})

	listRecorder := request(handler, http.MethodGet, "/rooms")
	var listObject struct {
		Rooms []json.RawMessage `json:"rooms"`
	}
	decodeResponse(t, listRecorder, &listObject)
	var listedRoom map[string]json.RawMessage
	if err := json.Unmarshal(listObject.Rooms[0], &listedRoom); err != nil {
		t.Fatalf("decode listed room: %v", err)
	}
	assertPlayersCharacterTypeJSON(t, listedRoom["players"], "characterType", "CharacterType", []simulation.CharacterType{simulation.CharacterTypeColt})

	detailRecorder := request(handler, http.MethodGet, "/rooms/"+mustRoomIDFromRaw(t, joinObject["room"]))
	var detailObject map[string]json.RawMessage
	decodeResponse(t, detailRecorder, &detailObject)
	assertPlayersCharacterTypeJSON(t, detailObject["players"], "characterType", "CharacterType", []simulation.CharacterType{simulation.CharacterTypeColt})

	debugRoom := createRoom(t, handler)
	debugPlayerRecorder := request(handler, http.MethodPost, "/rooms/"+debugRoom.ID+"/players")
	var debugSession map[string]json.RawMessage
	decodeResponse(t, debugPlayerRecorder, &debugSession)
	assertCharacterTypeJSONKey(t, debugSession["player"], "characterType", "CharacterType", simulation.CharacterTypeShelly)

	startRecorder := request(handler, http.MethodPost, "/rooms/"+debugRoom.ID+"/start")
	var startObject map[string]json.RawMessage
	decodeResponse(t, startRecorder, &startObject)
	assertPlayersCharacterTypeJSON(t, startObject["players"], "characterType", "CharacterType", []simulation.CharacterType{simulation.CharacterTypeShelly})
}

func TestCharacterTypeProjectsToReadyAndSimulationPlayers(t *testing.T) {
	config, err := simulation.StaticGameConfig().SelectMode(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("select Duel config: %v", err)
	}
	participants := []playerResponse{
		{ID: "human", Team: string(simulation.TeamRed), Slot: 0, CharacterType: simulation.CharacterTypeColt},
		{ID: "bot", Team: string(simulation.TeamBlue), Slot: 0, IsBot: true, CharacterType: simulation.CharacterTypeShelly},
	}

	ready := readyEventPlayers(participants, config)
	players := simulationPlayers(participants, config)
	if len(ready) != 2 || ready[0].CharacterType != simulation.CharacterTypeColt || ready[1].CharacterType != simulation.CharacterTypeShelly {
		t.Fatalf("Ready CharacterType projection = %+v", ready)
	}
	if len(players) != 2 || players[0].CharacterType != simulation.CharacterTypeColt || players[0].HP != 3100 || players[1].CharacterType != simulation.CharacterTypeShelly || players[1].HP != 4000 {
		t.Fatalf("simulation CharacterType/stat projection = %+v", players)
	}
	assertCharacterTypeJSONKey(t, mustMarshalTestJSON(t, ready[0]), "CharacterType", "characterType", simulation.CharacterTypeColt)
	assertCharacterTypeJSONKey(t, mustMarshalTestJSON(t, players[1]), "CharacterType", "characterType", simulation.CharacterTypeShelly)
}

func TestBotAndDebugParticipantsDefaultToShelly(t *testing.T) {
	t.Run("manual bot and debug player", func(t *testing.T) {
		store := NewStore(5)
		handler := debugHandler(t, store)
		room := createRoom(t, handler)
		debugPlayer := createPlayer(t, handler, room.ID)
		bots, err := store.addBots(room.ID, 1)
		if err != nil {
			t.Fatalf("add bot: %v", err)
		}
		if debugPlayer.CharacterType != simulation.CharacterTypeShelly || len(bots) != 1 || bots[0].CharacterType != simulation.CharacterTypeShelly {
			t.Fatalf("manual defaults: player=%+v bots=%+v", debugPlayer, bots)
		}
	})

	t.Run("automatic bot fill", func(t *testing.T) {
		clock := newFakeClock()
		store := NewStoreWithClock(5, clock)
		t.Cleanup(store.Close)
		joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
		if err != nil {
			t.Fatalf("join matchmaking: %v", err)
		}
		room := store.lookupRoom(joined.Room.ID)
		clock.TickTicker(matchmakingBotFillDelay, 0)
		waitForBotFillMatchStatus(t, room, MatchStatusMatched)
		room.mu.Lock()
		players := append([]playerResponse(nil), room.Players...)
		room.mu.Unlock()
		if len(players) != 2 || !players[1].IsBot || players[1].CharacterType != simulation.CharacterTypeShelly {
			t.Fatalf("automatic bot defaults = %+v", players)
		}
	})
}

func TestWebSocketReadyAndFirstSnapshotPreserveMixedCharacterTypes(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()

	colt := joinMatchmakingWithCharacter(t, handler, simulation.CharacterTypeColt)
	lily := joinMatchmakingWithCharacter(t, handler, simulation.CharacterTypeLily)
	coltConn := dialIssuedPlayer(t, server.URL, colt.WebSocketPath)
	defer coltConn.Close(websocket.StatusNormalClosure, "")
	lilyConn := dialIssuedPlayer(t, server.URL, lily.WebSocketPath)
	defer lilyConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, colt.Room.ID, colt.Player.ID)
	waitForAttachedClient(t, store, lily.Room.ID, lily.Player.ID)

	coltReadyPayload := readWebSocketPayload(t, coltConn)
	lilyReadyPayload := readWebSocketPayload(t, lilyConn)
	var coltReady, lilyReady readyEventMessage
	if err := json.Unmarshal(coltReadyPayload, &coltReady); err != nil {
		t.Fatalf("decode Colt Ready: %v", err)
	}
	if err := json.Unmarshal(lilyReadyPayload, &lilyReady); err != nil {
		t.Fatalf("decode Lily Ready: %v", err)
	}
	assertMatchingReadyEvents(t, coltReady, lilyReady)
	assertReadyCharacterType(t, coltReady.Players, colt.Player.ID, simulation.CharacterTypeColt)
	assertReadyCharacterType(t, coltReady.Players, lily.Player.ID, simulation.CharacterTypeLily)
	assertPlayersCharacterTypeEnvelope(t, coltReadyPayload, "Players", "CharacterType", "characterType", []simulation.CharacterType{simulation.CharacterTypeColt, simulation.CharacterTypeLily})

	writeWSJSON(t, coltConn, readyMessage{Type: "ready"})
	writeWSJSON(t, lilyConn, readyMessage{Type: "ready"})
	waitForMatchLifecycleState(t, store, colt.Room.ID, MatchStatusStarting, 2, 2)
	_ = readMatchSnapshotMessage(t, coltConn)
	_ = readMatchSnapshotMessage(t, lilyConn)
	for range matchCountdownSeconds {
		clock.TickTicker(time.Second, 0)
	}
	waitForMatchLifecycleState(t, store, colt.Room.ID, MatchStatusStarted, 2, 2)
	_ = readMatchSnapshotMessage(t, coltConn)
	_ = readMatchSnapshotMessage(t, lilyConn)
	clock.TickTicker(gameplayInterval, 0)
	coltSnapshot := readSnapshotMessage(t, coltConn)
	lilySnapshot := readSnapshotMessage(t, lilyConn)
	assertMatchingSnapshots(t, coltSnapshot, lilySnapshot)
	assertSnapshotCharacterTypeAndHP(t, coltSnapshot.Snapshot, colt.Player.ID, simulation.CharacterTypeColt, 3100)
	assertSnapshotCharacterTypeAndHP(t, coltSnapshot.Snapshot, lily.Player.ID, simulation.CharacterTypeLily, 4100)
	assertPlayersCharacterTypeEnvelope(t, mustMarshalTestJSON(t, coltSnapshot), "Players", "CharacterType", "characterType", []simulation.CharacterType{simulation.CharacterTypeColt, simulation.CharacterTypeLily})
}

func TestLifecycleControlSnapshotsKeepPlayersNullWithCharacterContract(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()

	joined := joinMatchmakingWithCharacter(t, handler, simulation.CharacterTypeColt)
	conn := dialIssuedPlayer(t, server.URL, joined.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, joined.Room.ID, joined.Player.ID)
	if bots, err := store.addBots(joined.Room.ID, 1); err != nil || len(bots) != 1 {
		t.Fatalf("fill Duel with bot: bots=%+v err=%v", bots, err)
	}
	_ = readReadyEventMessage(t, conn)
	writeWSJSON(t, conn, readyMessage{Type: "ready"})
	waitForMatchLifecycleState(t, store, joined.Room.ID, MatchStatusStarting, 1, 1)
	assertControlSnapshotPlayersNull(t, readWebSocketPayload(t, conn), MatchStatusStarting)
	for range matchCountdownSeconds {
		clock.TickTicker(time.Second, 0)
	}
	waitForMatchLifecycleState(t, store, joined.Room.ID, MatchStatusStarted, 1, 1)
	assertControlSnapshotPlayersNull(t, readWebSocketPayload(t, conn), MatchStatusStarted)
}

func TestWebSocketReconnectPreservesCanonicalCharacterType(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	defer server.Close()

	colt := joinMatchmakingWithCharacter(t, handler, simulation.CharacterTypeColt)
	firstConn := dialIssuedPlayer(t, server.URL, colt.WebSocketPath)
	waitForAttachedClient(t, store, colt.Room.ID, colt.Player.ID)
	if err := firstConn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("close unmatched Colt connection: %v", err)
	}
	waitForDetachedClient(t, store, colt.Room.ID, colt.Player.ID)

	reconnected := dialIssuedPlayer(t, server.URL, colt.WebSocketPath)
	defer reconnected.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, colt.Room.ID, colt.Player.ID)
	lily := joinMatchmakingWithCharacter(t, handler, simulation.CharacterTypeLily)
	lilyConn := dialIssuedPlayer(t, server.URL, lily.WebSocketPath)
	defer lilyConn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, lily.Room.ID, lily.Player.ID)

	coltReady := readReadyEventMessage(t, reconnected)
	_ = readReadyEventMessage(t, lilyConn)
	assertReadyCharacterType(t, coltReady.Players, colt.Player.ID, simulation.CharacterTypeColt)
	writeWSJSON(t, reconnected, readyMessage{Type: "ready"})
	writeWSJSON(t, lilyConn, readyMessage{Type: "ready"})
	waitForMatchLifecycleState(t, store, colt.Room.ID, MatchStatusStarting, 2, 2)
	_ = readMatchSnapshotMessage(t, reconnected)
	_ = readMatchSnapshotMessage(t, lilyConn)
	for range matchCountdownSeconds {
		clock.TickTicker(time.Second, 0)
	}
	waitForMatchLifecycleState(t, store, colt.Room.ID, MatchStatusStarted, 2, 2)
	_ = readMatchSnapshotMessage(t, reconnected)
	_ = readMatchSnapshotMessage(t, lilyConn)
	clock.TickTicker(gameplayInterval, 0)
	snapshot := readSnapshotMessage(t, reconnected)
	_ = readSnapshotMessage(t, lilyConn)
	assertSnapshotCharacterTypeAndHP(t, snapshot.Snapshot, colt.Player.ID, simulation.CharacterTypeColt, 3100)
}

func joinMatchmakingWithCharacter(t *testing.T, handler http.Handler, characterType simulation.CharacterType) matchmakingJoinResponse {
	t.Helper()
	body := `{"gameMode":"duel_1v1","characterType":` + strconv.Itoa(int(characterType)) + `}`
	recorder := requestWithBody(handler, http.MethodPost, "/matchmaking/join", body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("join CharacterType %d status=%d body=%s", characterType, recorder.Code, recorder.Body.String())
	}
	var joined matchmakingJoinResponse
	decodeResponse(t, recorder, &joined)
	return joined
}

func assertReadyCharacterType(t *testing.T, players []readyEventPlayer, playerID string, want simulation.CharacterType) {
	t.Helper()
	for _, player := range players {
		if player.ID == playerID {
			if player.CharacterType != want {
				t.Fatalf("Ready player %q CharacterType=%d, want %d", playerID, player.CharacterType, want)
			}
			return
		}
	}
	t.Fatalf("Ready player %q missing from %+v", playerID, players)
}

func assertSnapshotCharacterTypeAndHP(t *testing.T, snapshot simulation.Snapshot, playerID string, wantType simulation.CharacterType, wantHP float64) {
	t.Helper()
	player := findSnapshotPlayer(t, snapshot, simulation.PlayerID(playerID))
	if player.CharacterType != wantType || player.HP != wantHP {
		t.Fatalf("snapshot player %q CharacterType/HP=%d/%v, want %d/%v", playerID, player.CharacterType, player.HP, wantType, wantHP)
	}
}

func assertPlayersCharacterTypeEnvelope(t *testing.T, payload []byte, playersKey string, wantKey string, forbiddenKey string, want []simulation.CharacterType) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if snapshotRaw, ok := envelope["Snapshot"]; ok {
		if err := json.Unmarshal(snapshotRaw, &envelope); err != nil {
			t.Fatalf("decode Snapshot envelope: %v", err)
		}
	}
	assertPlayersCharacterTypeJSON(t, envelope[playersKey], wantKey, forbiddenKey, want)
}

func assertCharacterTypeJSONKey(t *testing.T, raw json.RawMessage, wantKey string, forbiddenKey string, want simulation.CharacterType) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode player object: %v", err)
	}
	payload, ok := object[wantKey]
	if !ok {
		t.Fatalf("missing %q in %s", wantKey, raw)
	}
	if _, exists := object[forbiddenKey]; exists {
		t.Fatalf("forbidden casing %q in %s", forbiddenKey, raw)
	}
	var got simulation.CharacterType
	if err := json.Unmarshal(payload, &got); err != nil || got != want {
		t.Fatalf("%s=%s, want %d (err=%v)", wantKey, payload, want, err)
	}
}

func assertPlayersCharacterTypeJSON(t *testing.T, raw json.RawMessage, wantKey string, forbiddenKey string, want []simulation.CharacterType) {
	t.Helper()
	var players []json.RawMessage
	if err := json.Unmarshal(raw, &players); err != nil {
		t.Fatalf("decode players: %v", err)
	}
	if len(players) != len(want) {
		t.Fatalf("players=%d, want %d", len(players), len(want))
	}
	for index := range players {
		assertCharacterTypeJSONKey(t, players[index], wantKey, forbiddenKey, want[index])
	}
}

func mustRoomIDFromRaw(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var room struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &room); err != nil {
		t.Fatalf("decode room ID: %v", err)
	}
	return room.ID
}

func TestMatchmakingCharacterTypeContract(t *testing.T) {
	tests := []struct {
		name string
		body string
		want simulation.CharacterType
	}{
		{name: "no body", body: "", want: simulation.CharacterTypeShelly},
		{name: "empty object", body: `{}`, want: simulation.CharacterTypeShelly},
		{name: "mode only", body: `{"gameMode":"solo"}`, want: simulation.CharacterTypeShelly},
		{name: "shelly", body: `{"characterType":0}`, want: simulation.CharacterTypeShelly},
		{name: "colt", body: `{"characterType":1}`, want: simulation.CharacterTypeColt},
		{name: "lily", body: `{"characterType":2}`, want: simulation.CharacterTypeLily},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			t.Cleanup(store.Close)
			recorder := requestWithBody(debugHandler(t, store), http.MethodPost, "/matchmaking/join", tt.body)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			var joined matchmakingJoinResponse
			decodeResponse(t, recorder, &joined)
			if joined.Player.CharacterType != tt.want || len(joined.Room.Players) != 1 || joined.Room.Players[0].CharacterType != tt.want {
				t.Fatalf("CharacterType was not canonical: %+v", joined)
			}
			stored := store.lookupRoom(joined.Room.ID)
			stored.mu.Lock()
			got := stored.Players[0].CharacterType
			stored.mu.Unlock()
			if got != tt.want {
				t.Fatalf("stored CharacterType = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMatchmakingInvalidCharacterTypeDoesNotMutate(t *testing.T) {
	invalid := []string{"null", `"1"`, "true", `{}`, `[]`, "1.5", "-1", "3", "9223372036854775808"}
	for _, value := range invalid {
		t.Run(value, func(t *testing.T) {
			store := NewStore(5)
			t.Cleanup(store.Close)
			recorder := requestWithBody(debugHandler(t, store), http.MethodPost, "/matchmaking/join", `{"gameMode":"duel_1v1","characterType":`+value+`}`)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			assertError(t, recorder, "invalid_character_type")
			if len(store.listRooms().Rooms) != 0 {
				t.Fatal("invalid CharacterType created a room")
			}
			store.mu.RLock()
			playerIDs, sessions := len(store.playerIDs), len(store.activeSessions)
			store.mu.RUnlock()
			if playerIDs != 0 || sessions != 0 {
				t.Fatalf("invalid CharacterType mutated IDs/sessions: %d/%d", playerIDs, sessions)
			}
		})
	}
}

func TestMatchmakingCharacterTypeErrorPriority(t *testing.T) {
	tests := []struct{ name, body, wantCode string }{
		{"invalid game mode before character", `{"gameMode":"ranked","characterType":3}`, "invalid_game_mode"},
		{"game mode shape before character", `{"gameMode":1,"characterType":3}`, "invalid_request"},
		{"malformed before character", `{"gameMode":"duel_1v1","characterType":`, "invalid_request"},
		{"valid mode then character", `{"gameMode":"duel_1v1","characterType":3}`, "invalid_character_type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			t.Cleanup(store.Close)
			recorder := requestWithBody(debugHandler(t, store), http.MethodPost, "/matchmaking/join", tt.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			assertError(t, recorder, tt.wantCode)
		})
	}
}
