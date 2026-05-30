package simulation

type Tick uint64

type PlayerID string

type Team string

const (
	TeamRed  Team = "red"
	TeamBlue Team = "blue"
)

type Vector2 struct {
	X float64
	Y float64
}

type InputCommand struct {
	PlayerID PlayerID
	Move     Vector2
}

type PlayerState struct {
	ID       PlayerID
	Team     Team
	Slot     int
	Position Vector2
}

type Snapshot struct {
	Tick    Tick
	Players []PlayerState
}

type State struct {
	tick    Tick
	players []PlayerState
}

func NewState(players []PlayerState) *State {
	return &State{
		players: clonePlayers(players),
	}
}

func (s *State) Step(inputs []InputCommand) Snapshot {
	_ = inputs

	s.tick++

	return Snapshot{
		Tick:    s.tick,
		Players: clonePlayers(s.players),
	}
}

func clonePlayers(players []PlayerState) []PlayerState {
	if len(players) == 0 {
		return nil
	}

	cloned := make([]PlayerState, len(players))
	copy(cloned, players)
	return cloned
}
