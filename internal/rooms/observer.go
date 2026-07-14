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

type observationPublication struct {
	transition observationTransition
	done       chan struct{}
}

type observationState struct {
	mu                sync.Mutex
	publishMu         sync.Mutex
	observer          Observer
	sequence          uint64
	activeRooms       int
	connectedClients  int
	publishedSequence [2]uint64
	pending           []observationPublication
	draining          bool
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
// Store or room core lock. It returns only after this transition's callback or
// stale drop completes, even when another goroutine owns the queue drainer.
//
// Observer callbacks are bounded pure sinks. They must not call Store methods
// or reenter observation publication. Sequences keep a delayed transition from
// overwriting a newer value.
func (s *observationState) publish(transition observationTransition) {
	publication := observationPublication{
		transition: transition,
		done:       make(chan struct{}),
	}
	s.publishMu.Lock()
	s.pending = append(s.pending, publication)
	if s.draining {
		s.publishMu.Unlock()
		<-publication.done
		return
	}
	s.draining = true
	s.publishMu.Unlock()

	var firstPanic any
	hasPanic := false
	for {
		s.publishMu.Lock()
		if len(s.pending) == 0 {
			s.draining = false
			s.publishMu.Unlock()
			if hasPanic {
				panic(firstPanic)
			}
			return
		}
		publication := s.pending[0]
		if len(s.pending) == 1 {
			s.pending = nil
		} else {
			s.pending = s.pending[1:]
		}
		transition := publication.transition
		stale := transition.sequence <= s.publishedSequence[transition.kind]
		if !stale {
			s.publishedSequence[transition.kind] = transition.sequence
		}
		s.publishMu.Unlock()

		if !stale {
			panicValue, panicked := captureCallbackPanic(func() {
				if transition.kind == activeRoomsObservation {
					s.observer.SetActiveRooms(transition.value)
					return
				}
				s.observer.SetConnectedClients(transition.value)
			})
			if panicked && !hasPanic {
				firstPanic = panicValue
				hasPanic = true
			}
		}
		close(publication.done)
	}
}

// captureCallbackPanic lets a single drainer finish its ready queue before it
// re-propagates the first external callback panic.
func captureCallbackPanic(callback func()) (panicValue any, panicked bool) {
	panicked = true
	defer func() {
		panicValue = recover()
	}()
	callback()
	panicked = false
	return nil, false
}

// observeTick invokes the external Observer and must be called without a Store
// or room core lock.
func (s *observationState) observeTick(duration time.Duration) {
	s.observer.ObserveTick(duration)
}
