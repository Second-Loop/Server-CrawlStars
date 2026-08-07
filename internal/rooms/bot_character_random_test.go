package rooms

import (
	"errors"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

type botCharacterSequence struct {
	values []simulation.CharacterType
	index  int
}

func shellyBotCharacterChooser() (simulation.CharacterType, error) {
	return simulation.CharacterTypeShelly, nil
}

type distinctIdentityReader struct {
	values []byte
	calls  int
}

func (reader *distinctIdentityReader) Read(buffer []byte) (int, error) {
	if reader.calls >= len(reader.values) {
		return 0, errors.New("identity entropy exhausted")
	}
	for index := range buffer {
		buffer[index] = reader.values[reader.calls]
	}
	reader.calls++
	return len(buffer), nil
}

func (sequence *botCharacterSequence) next() (simulation.CharacterType, error) {
	if sequence.index >= len(sequence.values) {
		return 0, errors.New("bot character sequence exhausted")
	}
	value := sequence.values[sequence.index]
	sequence.index++
	return value, nil
}

func TestManualBotCharacterChooserIsIndependentAndAllowsDuplicates(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		humanCount int
		want       []simulation.CharacterType
	}{
		{
			name:       "duel",
			mode:       simulation.GameModeDuel1v1,
			humanCount: 1,
			want:       []simulation.CharacterType{simulation.CharacterTypeColt},
		},
		{
			name:       "solo",
			mode:       simulation.GameModeSolo,
			humanCount: 1,
			want: []simulation.CharacterType{
				simulation.CharacterTypeColt,
				simulation.CharacterTypeLily,
				simulation.CharacterTypeColt,
				simulation.CharacterTypeShelly,
				simulation.CharacterTypeLily,
			},
		},
		{
			name:       "team",
			mode:       simulation.GameModeTeam,
			humanCount: 2,
			want: []simulation.CharacterType{
				simulation.CharacterTypeColt,
				simulation.CharacterTypeLily,
				simulation.CharacterTypeColt,
				simulation.CharacterTypeShelly,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chooser := &botCharacterSequence{values: tt.want}
			store := NewStoreWithConfig(5, StoreConfig{BotCharacterChooser: chooser.next})
			t.Cleanup(store.Close)

			var joined matchmakingJoinResponse
			for index := 0; index < tt.humanCount; index++ {
				var err error
				joined, err = store.joinMatchmaking(tt.mode)
				if err != nil {
					t.Fatalf("join human %d: %v", index, err)
				}
			}
			bots, err := store.addBots(joined.Room.ID, len(tt.want))
			if err != nil {
				t.Fatalf("add bots: %v", err)
			}
			if len(bots) != len(tt.want) {
				t.Fatalf("added %d bots, want %d", len(bots), len(tt.want))
			}
			if chooser.index != len(tt.want) {
				t.Fatalf("chooser calls=%d, want %d", chooser.index, len(tt.want))
			}

			room := store.lookupRoom(joined.Room.ID)
			if room == nil {
				t.Fatal("expected bot room")
			}
			room.mu.Lock()
			players := append([]playerResponse(nil), room.Players...)
			room.mu.Unlock()
			if len(players) != tt.humanCount+len(tt.want) {
				t.Fatalf("players=%d, want %d", len(players), tt.humanCount+len(tt.want))
			}
			for index, want := range tt.want {
				got := players[tt.humanCount+index]
				if !got.IsBot || got.CharacterType != want {
					t.Fatalf("bot[%d]=%+v, want IsBot and CharacterType=%d", index, got, want)
				}
			}
		})
	}
}

func TestBotFillUsesInjectedCharacterChooserAcrossModes(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		humanCount int
		want       []simulation.CharacterType
	}{
		{
			name:       "duel",
			mode:       simulation.GameModeDuel1v1,
			humanCount: 1,
			want:       []simulation.CharacterType{simulation.CharacterTypeColt},
		},
		{
			name:       "solo",
			mode:       simulation.GameModeSolo,
			humanCount: 1,
			want: []simulation.CharacterType{
				simulation.CharacterTypeColt,
				simulation.CharacterTypeLily,
				simulation.CharacterTypeColt,
				simulation.CharacterTypeShelly,
				simulation.CharacterTypeLily,
			},
		},
		{
			name:       "team",
			mode:       simulation.GameModeTeam,
			humanCount: 2,
			want: []simulation.CharacterType{
				simulation.CharacterTypeColt,
				simulation.CharacterTypeLily,
				simulation.CharacterTypeColt,
				simulation.CharacterTypeShelly,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newFakeClock()
			chooser := &botCharacterSequence{values: tt.want}
			store := newStore(5, clock, StoreConfig{BotCharacterChooser: chooser.next})
			t.Cleanup(store.Close)

			var joined matchmakingJoinResponse
			for index := 0; index < tt.humanCount; index++ {
				var err error
				joined, err = store.joinMatchmaking(tt.mode)
				if err != nil {
					t.Fatalf("join human %d: %v", index, err)
				}
			}

			clock.TickTicker(matchmakingBotFillDelay, 0)
			room := store.lookupRoom(joined.Room.ID)
			waitForBotFillMatchStatus(t, room, MatchStatusMatched)
			if chooser.index != len(tt.want) {
				t.Fatalf("chooser calls=%d, want %d", chooser.index, len(tt.want))
			}

			room.mu.Lock()
			players := append([]playerResponse(nil), room.Players...)
			room.mu.Unlock()
			for index, want := range tt.want {
				got := players[tt.humanCount+index]
				if !got.IsBot || got.CharacterType != want {
					t.Fatalf("bot[%d]=%+v, want IsBot and CharacterType=%d", index, got, want)
				}
			}
		})
	}
}

func TestBotCharacterTypePropagatesToReadyAndGameplaySnapshot(t *testing.T) {
	chooser := &botCharacterSequence{values: []simulation.CharacterType{simulation.CharacterTypeLily}}
	store := NewStoreWithConfig(5, StoreConfig{BotCharacterChooser: chooser.next})
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join human: %v", err)
	}
	bots, err := store.addBots(joined.Room.ID, 1)
	if err != nil {
		t.Fatalf("add bot: %v", err)
	}
	if len(bots) != 1 || bots[0].CharacterType != simulation.CharacterTypeLily {
		t.Fatalf("added bots=%+v, want Lily", bots)
	}

	room := store.lookupRoom(joined.Room.ID)
	if room == nil {
		t.Fatal("expected bot room")
	}
	if _, err := store.startRoom(room.ID); err != nil {
		t.Fatalf("start room: %v", err)
	}
	room.mu.Lock()
	players := append([]playerResponse(nil), room.Players...)
	ready := readyEventPlayers(players, room.gameConfig)
	snapshot := room.state.Step(nil)
	room.mu.Unlock()

	if got := ready[1].CharacterType; got != simulation.CharacterTypeLily {
		t.Fatalf("Ready bot CharacterType=%d, want Lily", got)
	}
	if got := snapshot.Players[1].CharacterType; got != simulation.CharacterTypeLily {
		t.Fatalf("gameplay Snapshot bot CharacterType=%d, want Lily", got)
	}
}

func TestDefaultBotCharacterChooserDoesNotConsumeStoreIdentityRandom(t *testing.T) {
	reader := &distinctIdentityReader{values: []byte{0x11, 0x22, 0x33, 0x44}}
	store := NewStoreWithConfig(5, StoreConfig{Random: reader})
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join human: %v", err)
	}
	if _, err := store.addBots(joined.Room.ID, 1); err != nil {
		t.Fatalf("default bot chooser consumed identity random: %v", err)
	}
	if reader.calls != len(reader.values) {
		t.Fatalf("identity random reads=%d, want exactly 4", reader.calls)
	}
}

func TestBotCharacterChooserFailureIsAtomic(t *testing.T) {
	chooserErr := errors.New("chooser unavailable")
	chooser := func() (simulation.CharacterType, error) {
		return 0, chooserErr
	}
	store := NewStoreWithConfig(5, StoreConfig{BotCharacterChooser: chooser})
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeSolo)
	if err != nil {
		t.Fatalf("join human: %v", err)
	}
	beforeIDs := snapshotBotFillPlayerIDs(store)
	room := store.lookupRoom(joined.Room.ID)
	room.mu.Lock()
	beforePlayers := len(room.Players)
	room.mu.Unlock()

	if _, err := store.addBots(joined.Room.ID, 2); !errors.Is(err, ErrInternal) {
		t.Fatalf("add bots error=%v, want ErrInternal", err)
	}
	room.mu.Lock()
	afterPlayers := len(room.Players)
	status := room.matchStatus
	room.mu.Unlock()
	if afterPlayers != beforePlayers || status != "" {
		t.Fatalf("failed add mutated room: players=%d/%d status=%q", afterPlayers, beforePlayers, status)
	}
	if afterIDs := snapshotBotFillPlayerIDs(store); len(afterIDs) != len(beforeIDs) {
		t.Fatalf("failed add leaked player IDs: before=%d after=%d", len(beforeIDs), len(afterIDs))
	}
}

func TestInvalidBotCharacterChooserValueIsRejectedAtomically(t *testing.T) {
	chooser := func() (simulation.CharacterType, error) {
		return simulation.CharacterType(99), nil
	}
	store := NewStoreWithConfig(5, StoreConfig{BotCharacterChooser: chooser})
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("join human: %v", err)
	}
	room := store.lookupRoom(joined.Room.ID)
	room.mu.Lock()
	beforePlayers := len(room.Players)
	room.mu.Unlock()
	beforeIDs := snapshotBotFillPlayerIDs(store)

	if _, err := store.addBots(joined.Room.ID, 1); !errors.Is(err, ErrInternal) {
		t.Fatalf("add bots error=%v, want ErrInternal", err)
	}
	room.mu.Lock()
	afterPlayers := len(room.Players)
	room.mu.Unlock()
	if afterPlayers != beforePlayers {
		t.Fatalf("invalid chooser mutated room: players=%d/%d", afterPlayers, beforePlayers)
	}
	if afterIDs := snapshotBotFillPlayerIDs(store); len(afterIDs) != len(beforeIDs) {
		t.Fatalf("invalid chooser leaked player IDs: before=%d after=%d", len(beforeIDs), len(afterIDs))
	}
}

func TestBotFillCharacterChooserFailureIsAtomic(t *testing.T) {
	clock := newFakeClock()
	logs := &lockedLogBuffer{}
	chooser := func() (simulation.CharacterType, error) {
		return 0, errors.New("chooser unavailable")
	}
	store := newStore(5, clock, StoreConfig{BotCharacterChooser: chooser, Logger: jsonTestLogger(logs)})
	t.Cleanup(store.Close)

	joined, err := store.joinMatchmaking(simulation.GameModeSolo)
	if err != nil {
		t.Fatalf("join human: %v", err)
	}
	room := store.lookupRoom(joined.Room.ID)
	beforeIDs := snapshotBotFillPlayerIDs(store)
	room.mu.Lock()
	beforePlayers := len(room.Players)
	room.mu.Unlock()

	clock.TickTicker(matchmakingBotFillDelay, 0)
	waitForBotFillLogEvent(t, logs, "bot_fill_failed")
	room.mu.Lock()
	afterPlayers := len(room.Players)
	status := room.matchStatus
	room.mu.Unlock()
	if afterPlayers != beforePlayers || status != "" {
		t.Fatalf("failed fill mutated room: players=%d/%d status=%q", afterPlayers, beforePlayers, status)
	}
	if afterIDs := snapshotBotFillPlayerIDs(store); len(afterIDs) != len(beforeIDs) {
		t.Fatalf("failed fill leaked player IDs: before=%d after=%d", len(beforeIDs), len(afterIDs))
	}
}
