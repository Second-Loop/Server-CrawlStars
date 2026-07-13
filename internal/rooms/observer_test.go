package rooms

import (
	"slices"
	"sync"
	"testing"
	"time"
)

func TestObservationTransitionsDropLateStalePublish(t *testing.T) {
	observer := &recordingObserver{}
	state := newObservationState(observer)
	earlier := state.activeRoomsDelta(1)
	later := state.activeRoomsDelta(1)

	if earlier.sequence >= later.sequence {
		t.Fatalf("expected monotonic sequences, got earlier=%d later=%d", earlier.sequence, later.sequence)
	}
	state.publish(later)
	state.publish(earlier)

	if got := observer.activeRoomValues(); !slices.Equal(got, []int{2}) {
		t.Fatalf("expected stale value to be dropped after publishing 2, got %v", got)
	}
}

func TestObservationCountersNeverGoNegativeOnDuplicateRelease(t *testing.T) {
	observer := &recordingObserver{}
	state := newObservationState(observer)
	transitions := []observationTransition{
		state.connectedClientsDelta(1),
		state.connectedClientsDelta(-1),
		state.connectedClientsDelta(-1),
	}

	for index, transition := range transitions {
		if transition.value < 0 {
			t.Fatalf("transition %d went negative: %+v", index, transition)
		}
		if index > 0 && transitions[index-1].sequence >= transition.sequence {
			t.Fatalf("expected monotonic sequences, got %d then %d", transitions[index-1].sequence, transition.sequence)
		}
		state.publish(transition)
	}

	if got := observer.connectedClientValues(); !slices.Equal(got, []int{1, 0, 0}) {
		t.Fatalf("expected duplicate release to stay at zero, got %v", got)
	}
}

func TestObservationNilObserverIsNormalizedToNoOp(t *testing.T) {
	state := newObservationState(nil)
	state.publish(state.activeRoomsDelta(1))
	state.publish(state.connectedClientsDelta(1))
	state.observeTick(time.Millisecond)
}

func TestObservationStateIsOwnedIndependentlyByEachStore(t *testing.T) {
	first := NewStore(1)
	second := NewStore(1)
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)

	if first.observation == nil || second.observation == nil {
		t.Fatal("expected every store to own observation state")
	}
	if first.observation == second.observation {
		t.Fatal("expected observation state to be isolated per store")
	}
	firstTransition := first.observation.activeRoomsDelta(1)
	secondTransition := second.observation.activeRoomsDelta(1)
	if firstTransition.value != 1 || firstTransition.sequence != 1 {
		t.Fatalf("unexpected first store transition: %+v", firstTransition)
	}
	if secondTransition.value != 1 || secondTransition.sequence != 1 {
		t.Fatalf("unexpected second store transition: %+v", secondTransition)
	}
}

type recordingObserver struct {
	mu               sync.Mutex
	activeRooms      []int
	connectedClients []int
	tickDurations    []time.Duration
}

func (o *recordingObserver) SetActiveRooms(count int) {
	o.mu.Lock()
	o.activeRooms = append(o.activeRooms, count)
	o.mu.Unlock()
}

func (o *recordingObserver) SetConnectedClients(count int) {
	o.mu.Lock()
	o.connectedClients = append(o.connectedClients, count)
	o.mu.Unlock()
}

func (o *recordingObserver) ObserveTick(duration time.Duration) {
	o.mu.Lock()
	o.tickDurations = append(o.tickDurations, duration)
	o.mu.Unlock()
}

func (o *recordingObserver) activeRoomValues() []int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.activeRooms)
}

func (o *recordingObserver) connectedClientValues() []int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.connectedClients)
}
