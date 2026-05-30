package simulation

import "testing"

func TestStepReturnsSnapshotWithoutTransport(t *testing.T) {
	state := NewState([]PlayerState{
		{
			ID:       PlayerID("red-1"),
			Team:     TeamRed,
			Slot:     0,
			Position: Vector2{X: -1, Y: 0},
		},
		{
			ID:       PlayerID("blue-1"),
			Team:     TeamBlue,
			Slot:     0,
			Position: Vector2{X: 1, Y: 0},
		},
	})

	snapshot := state.Step(nil)

	if snapshot.Tick != Tick(1) {
		t.Fatalf("expected first snapshot tick 1, got %d", snapshot.Tick)
	}
	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{X: -1, Y: 0})
	assertPlayer(t, snapshot, PlayerID("blue-1"), TeamBlue, 0, Vector2{X: 1, Y: 0})
}

func TestStepContractAcceptsInputCommands(t *testing.T) {
	state := NewState([]PlayerState{
		{
			ID:       PlayerID("red-1"),
			Team:     TeamRed,
			Slot:     0,
			Position: Vector2{X: 0, Y: 0},
		},
	})

	inputs := []InputCommand{
		{
			PlayerID: PlayerID("red-1"),
			Move:     Vector2{X: 1, Y: 0},
		},
	}

	snapshot := state.Step(inputs)

	if snapshot.Tick != Tick(1) {
		t.Fatalf("expected first snapshot tick 1, got %d", snapshot.Tick)
	}
	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{X: 0, Y: 0})
}

func TestTeamSlotsAreNotLimitedToOnePlayerPerTeam(t *testing.T) {
	state := NewState([]PlayerState{
		{ID: PlayerID("red-1"), Team: TeamRed, Slot: 0},
		{ID: PlayerID("red-2"), Team: TeamRed, Slot: 1},
		{ID: PlayerID("blue-1"), Team: TeamBlue, Slot: 0},
	})

	snapshot := state.Step(nil)

	if len(snapshot.Players) != 3 {
		t.Fatalf("expected 3 players in snapshot, got %d", len(snapshot.Players))
	}
	assertPlayer(t, snapshot, PlayerID("red-2"), TeamRed, 1, Vector2{})
}

func TestSnapshotDoesNotExposeMutableState(t *testing.T) {
	state := NewState([]PlayerState{
		{ID: PlayerID("red-1"), Team: TeamRed, Slot: 0},
	})

	first := state.Step(nil)
	first.Players[0].Position = Vector2{X: 99, Y: 99}

	second := state.Step(nil)

	if second.Tick != Tick(2) {
		t.Fatalf("expected second snapshot tick 2, got %d", second.Tick)
	}
	assertPlayer(t, second, PlayerID("red-1"), TeamRed, 0, Vector2{})
}

func TestNewStateDoesNotExposeInitialPlayerSlice(t *testing.T) {
	players := []PlayerState{
		{ID: PlayerID("red-1"), Team: TeamRed, Slot: 0},
	}

	state := NewState(players)
	players[0].Position = Vector2{X: 99, Y: 99}

	snapshot := state.Step(nil)

	assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, Vector2{})
}

func assertPlayer(t *testing.T, snapshot Snapshot, id PlayerID, team Team, slot int, position Vector2) {
	t.Helper()

	for _, player := range snapshot.Players {
		if player.ID != id {
			continue
		}
		if player.Team != team {
			t.Fatalf("expected %s team %q, got %q", id, team, player.Team)
		}
		if player.Slot != slot {
			t.Fatalf("expected %s slot %d, got %d", id, slot, player.Slot)
		}
		if player.Position != position {
			t.Fatalf("expected %s position %+v, got %+v", id, position, player.Position)
		}
		return
	}

	t.Fatalf("expected snapshot to include player %s", id)
}
