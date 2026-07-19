package rooms

import (
	"encoding/json"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestBotIdentityProjectsToReadyAndSnapshotMessages(t *testing.T) {
	config, err := simulation.StaticGameConfig().SelectMode(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("select Duel config: %v", err)
	}
	snapshotPlayers, readyPlayers := botProjectionFixture(config)
	participants := []playerResponse{
		{ID: "human", Team: string(simulation.TeamRed), Slot: 0, IsBot: false},
		{ID: "bot", Team: string(simulation.TeamBlue), Slot: 0, IsBot: true},
	}

	want := map[string]bool{"human": false, "bot": true}
	if len(snapshotPlayers) != len(want) {
		t.Fatalf("expected %d snapshot players, got %d", len(want), len(snapshotPlayers))
	}
	if len(readyPlayers) != len(want) {
		t.Fatalf("expected %d Ready players, got %d", len(want), len(readyPlayers))
	}

	for _, player := range snapshotPlayers {
		wantIsBot, ok := want[string(player.ID)]
		if !ok {
			t.Fatalf("unexpected snapshot player %q", player.ID)
		}
		if player.IsBot != wantIsBot {
			t.Fatalf("expected snapshot player %q IsBot %t, got %t", player.ID, wantIsBot, player.IsBot)
		}
		assertExactBotJSONKey(t, mustMarshalBotProjectionJSON(t, player), "IsBot", "isBot", wantIsBot)
	}

	for _, player := range readyPlayers {
		wantIsBot, ok := want[player.ID]
		if !ok {
			t.Fatalf("unexpected Ready player %q", player.ID)
		}
		if player.IsBot != wantIsBot {
			t.Fatalf("expected Ready player %q IsBot %t, got %t", player.ID, wantIsBot, player.IsBot)
		}
		assertExactBotJSONKey(t, mustMarshalBotProjectionJSON(t, player), "IsBot", "isBot", wantIsBot)
	}

	for _, player := range participants {
		assertExactBotJSONKey(t, mustMarshalBotProjectionJSON(t, player), "isBot", "IsBot", player.IsBot)
	}
}

func TestInputMessageDecodesClientTickAndDefaultsMissingToZero(t *testing.T) {
	var withTick inputMessage
	if err := json.Unmarshal([]byte(`{"ClientTick":42,"MoveDir":{"x":1,"y":0}}`), &withTick); err != nil {
		t.Fatalf("decode input with ClientTick: %v", err)
	}
	if withTick.ClientTick != 42 {
		t.Fatalf("ClientTick=%d, want 42", withTick.ClientTick)
	}

	var withoutTick inputMessage
	if err := json.Unmarshal([]byte(`{"MoveDir":{"x":1,"y":0}}`), &withoutTick); err != nil {
		t.Fatalf("decode legacy input without ClientTick: %v", err)
	}
	if withoutTick.ClientTick != 0 {
		t.Fatalf("missing ClientTick decoded as %d, want 0", withoutTick.ClientTick)
	}
}

func TestInputMessageRejectsExplicitNullOrNonIntegerClientTick(t *testing.T) {
	for name, payload := range map[string]string{
		"null":       `{"ClientTick":null}`,
		"fractional": `{"ClientTick":1.5}`,
		"string":     `{"ClientTick":"12"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var input inputMessage
			if err := json.Unmarshal([]byte(payload), &input); err == nil {
				t.Fatalf("decode %s ClientTick succeeded, want invalid input", name)
			}
		})
	}
}

func botProjectionFixture(config simulation.GameConfig) (
	[]simulation.PlayerData,
	[]readyEventPlayer,
) {
	participants := []playerResponse{
		{ID: "human", Team: string(simulation.TeamRed), Slot: 0, IsBot: false},
		{ID: "bot", Team: string(simulation.TeamBlue), Slot: 0, IsBot: true},
	}
	return simulationPlayers(participants, config), readyEventPlayers(participants, config)
}

func assertExactBotJSONKey(
	t *testing.T,
	raw []byte,
	wantKey string,
	forbiddenKey string,
	want bool,
) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode player object: %v", err)
	}
	value, ok := object[wantKey]
	if !ok {
		t.Fatalf("missing %q in %s", wantKey, raw)
	}
	if _, exists := object[forbiddenKey]; exists {
		t.Fatalf("forbidden casing %q in %s", forbiddenKey, raw)
	}
	var got bool
	if err := json.Unmarshal(value, &got); err != nil || got != want {
		t.Fatalf("%s=%s, want %t (err=%v)", wantKey, value, want, err)
	}
}

func mustMarshalBotProjectionJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal player object: %v", err)
	}
	return raw
}
