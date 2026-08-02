package rooms

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
	"nhooyr.io/websocket"
)

func TestWebSocketPressedSkillAppearsInAuthoritativeSnapshot(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Cleanup(store.Close)

	created := createRoom(t, handler)
	player := issuePlayer(t, handler, created.ID)
	startRoom(t, handler, created.ID)
	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	waitForAttachedClient(t, store, created.ID, player.ID)

	writeText(t, conn, `{"ClientTick":1,"AttackDir":{"x":0,"y":1},"PressedAttack":true,"PressedSkill":true}`)
	waitForPendingClientTick(t, store, created.ID, player.ID, 1)
	clock.TickTicker(gameplayInterval, 0)
	firstPayload := readWebSocketPayload(t, conn)
	var first snapshotMessage
	if err := json.Unmarshal(firstPayload, &first); err != nil {
		t.Fatalf("decode first skill snapshot: %v", err)
	}
	firstPlayer := findSnapshotPlayer(t, first.Snapshot, simulation.PlayerID(player.ID))
	if !firstPlayer.PressedSkill || firstPlayer.PressedAttack || firstPlayer.SkillReadyTick != 361 || firstPlayer.LastProcessedClientTick != 1 {
		t.Fatalf("first skill player=%+v", firstPlayer)
	}
	if len(first.Snapshot.Projectiles) != 0 {
		t.Fatalf("skill-first command created projectiles: %+v", first.Snapshot.Projectiles)
	}
	text := string(firstPayload)
	for _, want := range []string{`"PressedSkill":true`, `"SkillReadyTick":361`} {
		if !strings.Contains(text, want) {
			t.Fatalf("skill snapshot missing %s: %s", want, text)
		}
	}
	if strings.Contains(text, `"pressedSkill"`) || strings.Contains(text, `"skillReadyTick"`) {
		t.Fatalf("skill snapshot used lowercase field casing: %s", text)
	}

	clock.TickTicker(gameplayInterval, 0)
	second := readSnapshotMessage(t, conn)
	secondPlayer := findSnapshotPlayer(t, second.Snapshot, simulation.PlayerID(player.ID))
	if secondPlayer.PressedSkill || secondPlayer.SkillReadyTick != 361 {
		t.Fatalf("second skill player=%+v", secondPlayer)
	}
}

func TestWebSocketReconnectPreservesSkillReadyTick(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	handler := debugHandler(t, store)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Cleanup(store.Close)

	created := createRoom(t, handler)
	player := issuePlayer(t, handler, created.ID)
	startRoom(t, handler, created.ID)
	firstConn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	waitForAttachedClient(t, store, created.ID, player.ID)
	writeText(t, firstConn, `{"ClientTick":1,"AttackDir":{"x":1,"y":0},"PressedSkill":true}`)
	waitForPendingClientTick(t, store, created.ID, player.ID, 1)
	clock.TickTicker(gameplayInterval, 0)
	first := readSnapshotMessage(t, firstConn)
	if got := findSnapshotPlayer(t, first.Snapshot, simulation.PlayerID(player.ID)).SkillReadyTick; got != 361 {
		t.Fatalf("approved SkillReadyTick=%d, want 361", got)
	}
	if err := firstConn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("close started-room connection: %v", err)
	}
	waitForDetachedClient(t, store, created.ID, player.ID)

	reconnected := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	t.Cleanup(func() { _ = reconnected.Close(websocket.StatusNormalClosure, "") })
	waitForAttachedClient(t, store, created.ID, player.ID)
	clock.TickTicker(gameplayInterval, 0)
	afterReconnect := readSnapshotMessage(t, reconnected)
	reconnectedPlayer := findSnapshotPlayer(t, afterReconnect.Snapshot, simulation.PlayerID(player.ID))
	if reconnectedPlayer.PressedSkill || reconnectedPlayer.SkillReadyTick != 361 {
		t.Fatalf("reconnected skill player=%+v", reconnectedPlayer)
	}
}

func TestWebSocketInvalidPressedSkillPreservesPendingAndSnapshotStream(t *testing.T) {
	for name, value := range map[string]string{
		"null": "null", "number": "1", "string": `"true"`, "object": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newClientTickWebSocketFixture(t, 1)
			player := fixture.players[0]
			conn := fixture.connections[0]

			writeText(t, conn, `{"ClientTick":12,"MoveDir":{"x":1,"y":0},"AttackDir":{"x":0,"y":1},"PressedAttack":true,"PressedSkill":true}`)
			waitForPendingClientTick(t, fixture.store, fixture.room.ID, player.ID, 12)
			writeText(t, conn, `{"ClientTick":13,"PressedSkill":`+value+`}`)

			message := readErrorMessage(t, conn)
			if message.Type != "error" || message.Error.Code != "invalid_input" {
				t.Fatalf("%s PressedSkill error=%+v, want invalid_input", name, message)
			}
			room := fixture.store.lookupRoom(fixture.room.ID)
			room.mu.Lock()
			pending := room.pendingInputs[player.ID]
			room.mu.Unlock()
			want := simulation.InputCommand{
				PlayerID:      simulation.PlayerID(player.ID),
				ClientTick:    12,
				MoveDir:       simulation.Vector2{X: 1},
				AttackDir:     simulation.Vector2{Y: 1},
				PressedAttack: true,
				PressedSkill:  true,
			}
			if !reflect.DeepEqual(pending, want) {
				t.Fatalf("%s invalid PressedSkill mutated pending=%+v, want %+v", name, pending, want)
			}

			fixture.clock.TickTicker(gameplayInterval, 0)
			snapshot := readSnapshotMessage(t, conn)
			if snapshot.Type != "snapshot" || snapshot.Snapshot.Tick != 1 {
				t.Fatalf("%s invalid PressedSkill broke snapshot stream: %+v", name, snapshot)
			}
			if got := findSnapshotPlayer(t, snapshot.Snapshot, simulation.PlayerID(player.ID)).LastProcessedClientTick; got != 12 {
				t.Fatalf("%s processed ClientTick=%d, want 12", name, got)
			}
		})
	}
}

func TestSetInputCopiesPressedSkillAsPartOfWinningCommand(t *testing.T) {
	store, room, playerID, session := inputSelectionFixture(t)
	if got := store.setInput(room.ID, playerID, inputMessage{
		ClientTick:   12,
		MoveDir:      simulation.Vector2{X: 1},
		PressedSkill: true,
	}, session); got != inputStored {
		t.Fatalf("setInput tick 12 disposition=%v, want stored", got)
	}
	if got := store.setInput(room.ID, playerID, inputMessage{
		ClientTick:    13,
		MoveDir:       simulation.Vector2{Y: 1},
		AttackDir:     simulation.Vector2{X: -1},
		PressedAttack: true,
		PressedSkill:  false,
	}, session); got != inputStored {
		t.Fatalf("setInput tick 13 disposition=%v, want stored", got)
	}

	room.mu.Lock()
	pending := room.pendingInputs[playerID]
	room.mu.Unlock()
	want := simulation.InputCommand{
		PlayerID:      simulation.PlayerID(playerID),
		ClientTick:    13,
		MoveDir:       simulation.Vector2{Y: 1},
		AttackDir:     simulation.Vector2{X: -1},
		PressedAttack: true,
		PressedSkill:  false,
	}
	if !reflect.DeepEqual(pending, want) {
		t.Fatalf("pending input=%+v, want complete winning command %+v", pending, want)
	}
}
