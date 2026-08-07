package simulation

import (
	"testing"

	serverconfig "github.com/Second-Loop/Server-CrawlStars/server-config"
)

const productionMapSmokeTicks = 30

func TestProductionMapSixPlayersAt30HzSmoke(t *testing.T) {
	state, inputs := newProductionSixPlayerState(t)

	for tick := 1; tick <= productionMapSmokeTicks; tick++ {
		snapshot := state.Step(inputs)
		if snapshot.Tick != Tick(tick) {
			t.Fatalf("tick=%d, want %d", snapshot.Tick, tick)
		}
		if len(snapshot.Players) != 6 {
			t.Fatalf("tick=%d has %d players, want six", snapshot.Tick, len(snapshot.Players))
		}
		for _, player := range snapshot.Players {
			if player.IsDead || player.HP <= 0 {
				t.Fatalf("tick=%d unexpectedly killed player %+v", snapshot.Tick, player)
			}
		}
	}
}

func BenchmarkProductionMapSixPlayersAt30Hz(b *testing.B) {
	state, inputs := newProductionSixPlayerState(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for range productionMapSmokeTicks {
			state.Step(inputs)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N*productionMapSmokeTicks)/b.Elapsed().Seconds(), "ticks/s")
}

func newProductionSixPlayerState(t testing.TB) (*State, []InputCommand) {
	t.Helper()
	config, err := LoadGameConfig(serverconfig.Reader())
	if err != nil {
		t.Fatalf("load embedded production game config: %v", err)
	}
	config, err = config.SelectMode(GameModeSolo)
	if err != nil {
		t.Fatalf("select six-player solo mode: %v", err)
	}
	playerIDs := []PlayerID{"production-1", "production-2", "production-3", "production-4", "production-5", "production-6"}
	assignments := PlayerAssignments(playerIDs, config)
	if len(assignments) != len(playerIDs) {
		t.Fatalf("got %d player assignments, want six", len(assignments))
	}
	players := make([]PlayerData, len(assignments))
	for index, assignment := range assignments {
		players[index] = PlayerData{
			ID:            assignment.ID,
			Team:          assignment.Team,
			Slot:          assignment.Slot,
			CharacterType: CharacterType(index % 3),
			Pos:           assignment.SpawnPosition,
			MoveDir:       Vector2{X: 1},
			AttackDir:     Vector2{X: 1},
		}
	}
	inputs := make([]InputCommand, len(players))
	moveDirections := []Vector2{
		{X: 1},
		{X: -1},
		{Y: 1},
		{Y: -1},
		{X: 1, Y: 1},
		{X: -1, Y: -1},
	}
	for index, player := range players {
		inputs[index] = InputCommand{PlayerID: player.ID, MoveDir: moveDirections[index]}
	}
	return NewStateWithConfig(players, Config{Game: config}), inputs
}
