package rooms

import (
	"sync"
	"time"
)

type Observer interface {
	SetActiveRooms(int)
	SetConnectedClients(int)
	ObserveTick(time.Duration)
}

type noopObserver struct{}

func (noopObserver) SetActiveRooms(int) {}

func (noopObserver) SetConnectedClients(int) {}

func (noopObserver) ObserveTick(time.Duration) {}

type observationKind uint8

const (
	activeRoomsObservation observationKind = iota
	connectedClientsObservation
)

type observationTransition struct {
	kind     observationKind
	value    int
	sequence uint64
}

type observationState struct {
	mu                sync.Mutex
	publishMu         sync.Mutex
	observer          Observer
	sequence          uint64
	activeRooms       int
	connectedClients  int
	publishedSequence [2]uint64
}

func newObservationState(observer Observer) *observationState {
	return &observationState{observer: normalizeObserver(observer)}
}

func normalizeObserver(observer Observer) Observer {
	if observer == nil {
		return noopObserver{}
	}
	return observer
}

func (s *observationState) activeRoomsDelta(delta int) observationTransition {
	return s.transition(activeRoomsObservation, delta)
}

func (s *observationState) connectedClientsDelta(delta int) observationTransition {
	return s.transition(connectedClientsObservation, delta)
}

// transition records the counter value while a core Store or room transition
// is being committed. It never calls the external Observer.
func (s *observationState) transition(kind observationKind, delta int) observationTransition {
	s.mu.Lock()
	defer s.mu.Unlock()

	value := &s.activeRooms
	if kind == connectedClientsObservation {
		value = &s.connectedClients
	}
	*value += delta
	if *value < 0 {
		*value = 0
	}
	s.sequence++
	return observationTransition{kind: kind, value: *value, sequence: s.sequence}
}

// publish invokes the external Observer and must be called after releasing any
// Store or room core lock. Sequences keep a delayed transition from overwriting
// a newer value.
func (s *observationState) publish(transition observationTransition) {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()

	if transition.sequence <= s.publishedSequence[transition.kind] {
		return
	}
	s.publishedSequence[transition.kind] = transition.sequence
	if transition.kind == activeRoomsObservation {
		s.observer.SetActiveRooms(transition.value)
		return
	}
	s.observer.SetConnectedClients(transition.value)
}

// observeTick invokes the external Observer and must be called without a Store
// or room core lock.
func (s *observationState) observeTick(duration time.Duration) {
	s.observer.ObserveTick(duration)
}
