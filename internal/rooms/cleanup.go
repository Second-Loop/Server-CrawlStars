package rooms

import (
	"context"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const janitorInterval = 30 * time.Second
const shutdownWebSocketCloseReason = "server shutting down"

func (s *Store) Close() {
	_ = s.Shutdown(context.Background())
}

// Shutdown quiesces external mutations, closes every owned worker and
// connection, and publishes final lifecycle metrics before returning. The
// first caller owns the shutdown context; followers wait for the same result.
func (s *Store) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ownerDone := ctx.Done() // Every caller observes its context, but only the owner controls shutdown.
	owner := false
	s.shutdownOnce.Do(func() { owner = true })
	if owner {
		s.runShutdown(ctx, ownerDone)
	}
	<-s.shutdownDone
	if panicValue, panicked := s.shutdownPanicResult(); panicked {
		panic(panicValue)
	}
	return s.shutdownResult()
}

func (s *Store) runShutdown(ctx context.Context, ownerDone <-chan struct{}) {
	// The exclusive gate drains mutations that already entered. Releasing it
	// immediately after closed=true lets later calls acquire the shared gate and
	// return their documented quiescing result without waiting for teardown.
	s.mutationMu.Lock()
	s.mu.Lock()
	s.closed = true
	activeSessions, activeSessionDone := activeSessionSnapshotLocked(s.activeSessions)
	s.mu.Unlock()
	s.stopLaunchingRoomWorkers()
	s.mutationMu.Unlock()

	var resources roomResources
	var detachOnce sync.Once
	detachRooms := func() {
		detachOnce.Do(func() {
			resources = s.detachRoomsForShutdown()
		})
	}

	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ownerDone:
			s.setShutdownResult(ctx.Err())
			detachRooms()
			for _, session := range s.activeSessionSnapshot() {
				session.forceClose(websocket.StatusNormalClosure, shutdownWebSocketCloseReason)
			}
		case <-watchStop:
		}
	}()

	close(s.janitorStop)
	<-s.janitorDone

	detachRooms()
	resources.stop()

	allSessions := uniqueClientSessions(activeSessions, resources.sessions)
	closeClientSessionsInParallel(allSessions, websocket.StatusNormalClosure, shutdownWebSocketCloseReason)
	s.workerWG.Wait()
	waitClientSessions(allSessions)
	for _, lifecycleDone := range activeSessionDone {
		<-lifecycleDone
	}

	close(watchStop)
	<-watchDone
	if err := ctx.Err(); err != nil {
		s.setShutdownResult(err)
	}
	close(s.shutdownDone)
}

func (s *Store) detachRoomsForShutdown() roomResources {
	rooms := s.registeredRooms()
	var resources roomResources
	for _, room := range rooms {
		clientStart := len(resources.clientObservations)
		room.mu.Lock()
		playerIDs, removed := resources.removeRoomLocked(room)
		clientTransitions := s.clientObservationTransitionsLocked(resources.clientObservations[clientStart:], -1)
		room.mu.Unlock()
		for _, transition := range clientTransitions {
			observed := transition
			s.captureShutdownCallback(func() {
				s.publishDisconnectedClients([]clientObservationTransition{observed})
			})
		}
		if !removed {
			continue
		}
		activeTransition, deleted := s.removeRegisteredRoomIfSame(room.ID, room)
		if !deleted {
			continue
		}
		s.captureShutdownCallback(func() {
			s.observation.publish(activeTransition)
		})
		s.releasePlayerIDs(playerIDs)
	}
	return resources
}

func (s *Store) captureShutdownCallback(callback func()) {
	panicValue, panicked := captureCallbackPanic(callback)
	if panicked {
		s.setShutdownPanic(panicValue)
	}
}

func (s *Store) activeSessionSnapshot() []*clientSession {
	s.mu.RLock()
	sessions, _ := activeSessionSnapshotLocked(s.activeSessions)
	s.mu.RUnlock()
	return sessions
}

func activeSessionSnapshotLocked(active map[*clientSession]chan struct{}) ([]*clientSession, []<-chan struct{}) {
	sessions := make([]*clientSession, 0, len(active))
	lifecycleDone := make([]<-chan struct{}, 0, len(active))
	for session, done := range active {
		if session == nil {
			continue
		}
		sessions = append(sessions, session)
		lifecycleDone = append(lifecycleDone, done)
	}
	return sessions, lifecycleDone
}

func (s *Store) setShutdownResult(err error) {
	if err == nil {
		return
	}
	s.shutdownErrMu.Lock()
	if s.shutdownErr == nil {
		s.shutdownErr = err
	}
	s.shutdownErrMu.Unlock()
}

func (s *Store) shutdownResult() error {
	s.shutdownErrMu.Lock()
	defer s.shutdownErrMu.Unlock()
	return s.shutdownErr
}

func (s *Store) setShutdownPanic(panicValue any) {
	s.shutdownErrMu.Lock()
	if !s.shutdownPanicked {
		s.shutdownPanic = panicValue
		s.shutdownPanicked = true
	}
	s.shutdownErrMu.Unlock()
}

func (s *Store) shutdownPanicResult() (any, bool) {
	s.shutdownErrMu.Lock()
	defer s.shutdownErrMu.Unlock()
	return s.shutdownPanic, s.shutdownPanicked
}

func uniqueClientSessions(groups ...[]*clientSession) []*clientSession {
	seen := make(map[*clientSession]struct{})
	var unique []*clientSession
	for _, sessions := range groups {
		for _, session := range sessions {
			if session == nil {
				continue
			}
			if _, exists := seen[session]; exists {
				continue
			}
			seen[session] = struct{}{}
			unique = append(unique, session)
		}
	}
	return unique
}

func closeClientSessionsInParallel(sessions []*clientSession, code websocket.StatusCode, reason string) {
	var wait sync.WaitGroup
	for _, session := range uniqueClientSessions(sessions) {
		wait.Add(1)
		go func(session *clientSession) {
			defer wait.Done()
			session.close(code, reason)
		}(session)
	}
	wait.Wait()
}

func waitClientSessions(sessions []*clientSession) {
	for _, session := range sessions {
		if session == nil {
			continue
		}
		<-session.closeDone
		<-session.writerDone
		<-session.heartbeatDone
	}
}

func (s *Store) startJanitor() {
	ticker := s.clock.NewTicker(janitorInterval)
	go func() {
		defer close(s.janitorDone)
		defer ticker.Stop()

		for {
			select {
			case now := <-ticker.C():
				s.cleanupExpired(now)
			case <-s.janitorStop:
				return
			}
		}
	}()
}

func (s *Store) cleanupExpired(now time.Time) int {
	rooms := s.registeredRooms()
	deleted := 0
	var resources roomResources
	for _, room := range rooms {
		clientStart := len(resources.clientObservations)
		room.mu.Lock()
		if room.removed || !room.isExpired(now) {
			room.mu.Unlock()
			continue
		}
		playerIDs, removed := resources.removeRoomLocked(room)
		clientTransitions := s.clientObservationTransitionsLocked(resources.clientObservations[clientStart:], -1)
		room.mu.Unlock()
		s.publishDisconnectedClients(clientTransitions)
		if removed && s.deleteRoomIfSame(room.ID, room) {
			s.releasePlayerIDs(playerIDs)
			s.logRoomEvent("room_expired", room.ID)
			deleted++
		}
	}

	resources.close(defaultRoomWebSocketCloseMsg)
	return deleted
}

// isExpired requires r.mu because TTL eligibility depends on room-owned state.
func (r *room) isExpired(now time.Time) bool {
	if r.ending {
		return false
	}
	if !r.createdAt.IsZero() && !now.Before(r.createdAt.Add(defaultHardRoomLifetime)) {
		return true
	}
	if len(r.clients) > 0 || len(r.reservations) > 0 {
		return false
	}
	if r.Status == RoomStatusWaiting {
		return !now.Before(r.lastActivityAt.Add(defaultWaitingRoomIdleTTL))
	}
	if r.Status == RoomStatusStarted && !r.disconnectedAt.IsZero() {
		return !now.Before(r.disconnectedAt.Add(defaultDisconnectedRoomTTL))
	}
	return false
}

type roomResources struct {
	tickers            []ticker
	stops              []chan struct{}
	sessions           []*clientSession
	clientObservations []clientObservation
}

// detachGameplayLocked stops future simulation ticks without removing the room
// or its clients. The caller holds room.mu and invokes stop after unlocking.
func (r *roomResources) detachGameplayLocked(room *room) {
	if room.ticker != nil {
		r.tickers = append(r.tickers, room.ticker)
		room.ticker = nil
	}
	if room.stop != nil {
		r.stops = append(r.stops, room.stop)
		room.stop = nil
	}
}

func (s *Store) scheduleGameEndCleanup(room *room, sessions []*clientSession) bool {
	return s.launchRoomWorker(func() {
		for _, session := range uniqueClientSessions(sessions) {
			<-session.closeDone
		}
		s.finishGameEnd(room)
	})
}

// finishGameEnd owns only normal completion. The shared mutation gate makes
// normal cleanup and forced shutdown mutually exclusive; shutdown takeover,
// stale registry ownership, removed rooms, and callback panics never signal
// successful GameEnd cleanup.
func (s *Store) finishGameEnd(room *room) {
	if room == nil {
		return
	}

	s.mutationMu.RLock()
	defer s.mutationMu.RUnlock()

	var resources roomResources
	var clientTransitions []clientObservationTransition
	var activeTransition observationTransition
	var playerIDs []string

	s.mu.Lock()
	if s.closed || s.rooms[room.ID] != room {
		s.mu.Unlock()
		return
	}
	room.mu.Lock()
	if room.removed || !room.ending {
		room.mu.Unlock()
		s.mu.Unlock()
		return
	}
	clientStart := len(resources.clientObservations)
	var removed bool
	playerIDs, removed = resources.removeRoomLocked(room)
	if !removed {
		room.mu.Unlock()
		s.mu.Unlock()
		return
	}
	clientTransitions = s.clientObservationTransitionsLocked(resources.clientObservations[clientStart:], -1)
	delete(s.rooms, room.ID)
	activeTransition = s.observation.activeRoomsDelta(-1)
	room.mu.Unlock()
	s.mu.Unlock()

	s.publishDisconnectedClients(clientTransitions)
	s.observation.publish(activeTransition)
	s.releasePlayerIDs(playerIDs)
	s.logRoomEvent("room_ended", room.ID)
	resources.close(defaultGameEndCloseMsg)
	room.signalGameEndCleanupDone()
}

type clientObservation struct {
	roomID   string
	playerID string
	session  *clientSession
}

// removeRoomLocked marks a room unavailable and detaches resources for closing.
// The caller must hold room.mu and must release it before touching Store.mu.
func (r *roomResources) removeRoomLocked(room *room) ([]string, bool) {
	if room.removed {
		return nil, false
	}
	room.removed = true
	playerIDs := make([]string, 0, len(room.Players))
	for _, player := range room.Players {
		playerIDs = append(playerIDs, player.ID)
	}
	if room.countdownTicker != nil {
		r.tickers = append(r.tickers, room.countdownTicker)
		room.countdownTicker = nil
	}
	if room.countdownStop != nil {
		r.stops = append(r.stops, room.countdownStop)
		room.countdownStop = nil
	}
	if room.ticker != nil {
		r.tickers = append(r.tickers, room.ticker)
		room.ticker = nil
	}
	if room.stop != nil {
		r.stops = append(r.stops, room.stop)
		room.stop = nil
	}
	for playerID, session := range room.clients {
		if session != nil {
			r.sessions = append(r.sessions, session)
			r.clientObservations = append(r.clientObservations, clientObservation{
				roomID:   room.ID,
				playerID: playerID,
				session:  session,
			})
		}
	}
	room.clients = nil
	room.reservations = nil
	return playerIDs, true
}

func (r roomResources) close(reason string) {
	r.stop()
	for _, session := range r.sessions {
		session.close(websocket.StatusNormalClosure, reason)
	}
}

func (r roomResources) stop() {
	for _, ticker := range r.tickers {
		ticker.Stop()
	}
	for _, stop := range r.stops {
		close(stop)
	}
}
