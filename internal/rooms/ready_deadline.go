package rooms

import "time"

// armReadyDeadlineLocked starts the room-owned one-shot deadline for human
// Ready ACKs after the room enters Loading. The caller holds room.mu.
func (s *Store) armReadyDeadlineLocked(room *room) roomResources {
	var resources roomResources
	if room.removed || room.ending || room.matchStatus != MatchStatusLoading ||
		matchmakingHumanCount(room.Players) == 0 || room.allMatchPlayersReady() ||
		room.readyDeadlineTicker != nil {
		return resources
	}

	deadlineAt := s.clock.Now().Add(loadingReadyDeadline)
	deadlineTicker := s.clock.NewTicker(loadingReadyDeadline)
	deadlineStop := make(chan struct{})
	room.readyDeadlineTicker = deadlineTicker
	room.readyDeadlineStop = deadlineStop
	room.readyDeadlineAt = deadlineAt
	if !s.launchRoomWorker(func() { s.runReadyDeadline(room, deadlineTicker, deadlineStop) }) {
		resources.detachReadyDeadlineLocked(room)
	}
	return resources
}

func (s *Store) runReadyDeadline(room *room, deadlineTicker ticker, stop <-chan struct{}) {
	select {
	case <-deadlineTicker.C():
		s.expireReadyDeadline(room, deadlineTicker)
	case <-stop:
	}
}

func (s *Store) expireReadyDeadline(room *room, expectedTicker ticker) {
	if room == nil || expectedTicker == nil || !s.beginMutation() {
		return
	}
	var resources roomResources
	var playerIDs []string
	removed := false
	defer func() {
		if removed {
			s.releasePlayerIDs(playerIDs)
			resources.closeWithCause(defaultMatchCancelMsg, websocketCloseCausePrestartCancel)
		}
		s.endMutation()
	}()

	s.mu.Lock()
	if s.closed || s.rooms[room.ID] != room {
		s.mu.Unlock()
		return
	}
	room.mu.Lock()
	now := s.clock.Now()
	if !room.readyDeadlineExpiredLocked(now, expectedTicker) {
		room.mu.Unlock()
		s.mu.Unlock()
		return
	}
	var clientTransitions []clientObservationTransition
	var activeTransition observationTransition
	playerIDs, clientTransitions, activeTransition, removed = s.detachExpiredReadyDeadlineLocked(room, expectedTicker, now, &resources)
	room.mu.Unlock()
	s.mu.Unlock()

	if !removed {
		return
	}
	s.publishDisconnectedClients(clientTransitions)
	s.observation.publish(activeTransition)
	s.logMatchmakingTransition(room.ID, "cancelled", "ready_deadline_expired")
}

// readyDeadlineExpiredLocked keeps the absolute boundary authoritative even if
// the one-shot worker is scheduled late. The caller holds room.mu.
func (r *room) readyDeadlineExpiredLocked(now time.Time, expectedTicker ticker) bool {
	return !r.removed && !r.ending && r.matchStatus == MatchStatusLoading &&
		expectedTicker != nil && r.readyDeadlineTicker == expectedTicker &&
		!r.readyDeadlineAt.IsZero() && !now.Before(r.readyDeadlineAt)
}

// detachExpiredReadyDeadlineLocked removes an expired Loading room exactly
// once. The caller owns mutationMu and holds Store.mu before room.mu.
func (s *Store) detachExpiredReadyDeadlineLocked(
	room *room,
	expectedTicker ticker,
	now time.Time,
	resources *roomResources,
) ([]string, []clientObservationTransition, observationTransition, bool) {
	if s.closed || s.rooms[room.ID] != room ||
		!room.readyDeadlineExpiredLocked(now, expectedTicker) {
		return nil, nil, observationTransition{}, false
	}
	clientStart := len(resources.clientObservations)
	playerIDs, removed := resources.removeRoomLockedWithCause(room, websocketCloseCausePrestartCancel)
	if !removed {
		return nil, nil, observationTransition{}, false
	}
	clientTransitions := s.clientObservationTransitionsLocked(resources.clientObservations[clientStart:], -1)
	delete(s.rooms, room.ID)
	activeTransition := s.observation.activeRoomsDelta(-1)
	return playerIDs, clientTransitions, activeTransition, true
}
