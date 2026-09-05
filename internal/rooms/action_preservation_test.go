package rooms

import (
	"reflect"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestSetInputPreservesPendingPositiveActionAcrossMovement(t *testing.T) {
	store, room, playerID, session := inputSelectionFixture(t)

	if got := store.setInput(room.ID, playerID, inputMessage{
		ClientTick:    10,
		MoveDir:       simulation.Vector2{X: 1},
		AttackDir:     simulation.Vector2{Y: 1},
		PressedAttack: true,
	}, session); got != inputStored {
		t.Fatalf("setInput action disposition=%v, want stored", got)
	}
	if got := store.setInput(room.ID, playerID, inputMessage{
		ClientTick: 11,
		MoveDir:    simulation.Vector2{Y: 1},
	}, session); got != inputStored {
		t.Fatalf("setInput movement disposition=%v, want stored", got)
	}

	room.mu.Lock()
	pending := room.pendingInputs[playerID]
	room.mu.Unlock()
	want := simulation.InputCommand{
		PlayerID:      simulation.PlayerID(playerID),
		ClientTick:    11,
		MoveDir:       simulation.Vector2{Y: 1},
		AttackDir:     simulation.Vector2{Y: 1},
		PressedAttack: true,
	}
	if !reflect.DeepEqual(pending, want) {
		t.Fatalf("pending input=%+v, want movement with pending action %+v", pending, want)
	}
}

func TestSetInputUsesNewestPositiveActionAsWholeAction(t *testing.T) {
	store, room, playerID, session := inputSelectionFixture(t)

	inputs := []inputMessage{
		{ClientTick: 10, MoveDir: simulation.Vector2{X: 1}, AttackDir: simulation.Vector2{X: 1}, PressedAttack: true},
		{ClientTick: 11, MoveDir: simulation.Vector2{Y: 1}},
		{ClientTick: 12, MoveDir: simulation.Vector2{X: -1}, AttackDir: simulation.Vector2{Y: -1}, PressedSkill: true},
	}
	for _, input := range inputs {
		if got := store.setInput(room.ID, playerID, input, session); got != inputStored {
			t.Fatalf("setInput tick %d disposition=%v, want stored", input.ClientTick, got)
		}
	}

	room.mu.Lock()
	pending := room.pendingInputs[playerID]
	room.mu.Unlock()
	want := simulation.InputCommand{
		PlayerID:     simulation.PlayerID(playerID),
		ClientTick:   12,
		MoveDir:      simulation.Vector2{X: -1},
		AttackDir:    simulation.Vector2{Y: -1},
		PressedSkill: true,
	}
	if !reflect.DeepEqual(pending, want) {
		t.Fatalf("pending input=%+v, want newest action with newest movement %+v", pending, want)
	}
}

func TestSetInputIgnoresStaleAndDuplicateAfterActionPreservation(t *testing.T) {
	store, room, playerID, session := inputSelectionFixture(t)

	if got := store.setInput(room.ID, playerID, inputMessage{
		ClientTick:    10,
		MoveDir:       simulation.Vector2{X: 1},
		AttackDir:     simulation.Vector2{Y: 1},
		PressedAttack: true,
	}, session); got != inputStored {
		t.Fatalf("setInput action disposition=%v, want stored", got)
	}
	if got := store.setInput(room.ID, playerID, inputMessage{ClientTick: 12, MoveDir: simulation.Vector2{Y: 1}}, session); got != inputStored {
		t.Fatalf("setInput movement disposition=%v, want stored", got)
	}
	for _, tick := range []int64{11, 12} {
		if got := store.setInput(room.ID, playerID, inputMessage{ClientTick: tick, MoveDir: simulation.Vector2{X: -1}, PressedSkill: true}, session); got != inputIgnored {
			t.Fatalf("setInput stale tick %d disposition=%v, want ignored", tick, got)
		}
	}

	room.mu.Lock()
	pending := room.pendingInputs[playerID]
	room.mu.Unlock()
	if pending.ClientTick != 12 || pending.MoveDir != (simulation.Vector2{Y: 1}) || !pending.PressedAttack || pending.PressedSkill || pending.AttackDir != (simulation.Vector2{Y: 1}) {
		t.Fatalf("stale input mutated preserved pending=%+v", pending)
	}
}

func TestSetInputLegacyZeroKeepsWholeOverwriteCompatibility(t *testing.T) {
	tests := []struct {
		name   string
		first  inputMessage
		second inputMessage
		want   simulation.InputCommand
	}{
		{
			name:   "legacy replaces positive",
			first:  inputMessage{ClientTick: 10, MoveDir: simulation.Vector2{X: 1}, AttackDir: simulation.Vector2{Y: 1}, PressedAttack: true},
			second: inputMessage{MoveDir: simulation.Vector2{Y: 1}},
			want:   simulation.InputCommand{MoveDir: simulation.Vector2{Y: 1}},
		},
		{
			name:   "positive replaces legacy",
			first:  inputMessage{MoveDir: simulation.Vector2{X: 1}, AttackDir: simulation.Vector2{Y: 1}, PressedSkill: true},
			second: inputMessage{ClientTick: 11, MoveDir: simulation.Vector2{Y: 1}},
			want:   simulation.InputCommand{ClientTick: 11, MoveDir: simulation.Vector2{Y: 1}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, room, playerID, session := inputSelectionFixture(t)
			if got := store.setInput(room.ID, playerID, test.first, session); got != inputStored {
				t.Fatalf("first disposition=%v, want stored", got)
			}
			if got := store.setInput(room.ID, playerID, test.second, session); got != inputStored {
				t.Fatalf("second disposition=%v, want stored", got)
			}
			room.mu.Lock()
			pending := room.pendingInputs[playerID]
			room.mu.Unlock()
			test.want.PlayerID = simulation.PlayerID(playerID)
			if !reflect.DeepEqual(pending, test.want) {
				t.Fatalf("pending input=%+v, want whole overwrite %+v", pending, test.want)
			}
		})
	}
}

func TestSetInputConsumesPreservedActionOnceAndDoesNotReplayAfterCooldownRejection(t *testing.T) {
	store, room, playerID, session := inputSelectionFixture(t)

	if got := store.setInput(room.ID, playerID, inputMessage{
		ClientTick:   1,
		AttackDir:    simulation.Vector2{X: 1},
		PressedSkill: true,
	}, session); got != inputStored {
		t.Fatalf("skill disposition=%v, want stored", got)
	}
	store.tickRoomState(room)
	room.mu.Lock()
	first := room.lastPlayers[0]
	room.mu.Unlock()
	if !first.PressedSkill || first.SkillReadyTick == 0 {
		t.Fatalf("initial skill was not approved: %+v", first)
	}

	if got := store.setInput(room.ID, playerID, inputMessage{
		ClientTick: 2,
		MoveDir:    simulation.Vector2{Y: 1},
	}, session); got != inputStored {
		t.Fatalf("movement disposition=%v, want stored", got)
	}
	store.tickRoomState(room)

	room.mu.Lock()
	processed := room.lastPlayers[0]
	pendingAfterRejection := len(room.pendingInputs)
	room.mu.Unlock()
	if processed.PressedSkill {
		t.Fatal("cooldown-rejected preserved skill was approved again")
	}
	if processed.LastProcessedClientTick != 2 || processed.SkillReadyTick != first.SkillReadyTick {
		t.Fatalf("cooldown rejection state=%+v, want ACK 2 and unchanged ready tick %d", processed, first.SkillReadyTick)
	}
	if pendingAfterRejection != 0 {
		t.Fatalf("pending inputs after cooldown rejection=%v, want consumed", pendingAfterRejection)
	}

	store.tickRoomState(room)
	room.mu.Lock()
	if room.lastPlayers[0].PressedSkill {
		t.Fatal("rejected skill replayed on following tick")
	}
	room.mu.Unlock()
}

func TestSetInputDoesNotReplayPendingActionAfterDisconnect(t *testing.T) {
	store, room, playerID, session := inputSelectionFixture(t)
	if got := store.setInput(room.ID, playerID, inputMessage{
		ClientTick:   1,
		AttackDir:    simulation.Vector2{X: 1},
		PressedSkill: true,
	}, session); got != inputStored {
		t.Fatalf("skill disposition=%v, want stored", got)
	}

	store.releaseClient(&clientReservation{room: room, playerID: playerID}, session)
	store.tickRoomState(room)

	room.mu.Lock()
	defer room.mu.Unlock()
	if _, ok := room.pendingInputs[playerID]; ok {
		t.Fatal("disconnect retained pending action")
	}
	if room.lastPlayers[0].PressedSkill {
		t.Fatal("disconnected pending action replayed on a later tick")
	}
}
