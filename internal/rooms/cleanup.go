package rooms

import (
	"time"

	"nhooyr.io/websocket"
)

const janitorInterval = 30 * time.Second

func (s *Store) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		activeSessions := make([]*clientSession, 0, len(s.activeSessions))
		activeSessionDone := make([]<-chan struct{}, 0, len(s.activeSessions))
		for session, lifecycleDone := range s.activeSessions {
			activeSessions = append(activeSessions, session)
			activeSessionDone = append(activeSessionDone, lifecycleDone)
		}
		s.mu.Unlock()

		close(s.janitorStop)
		<-s.janitorDone

		rooms := s.registeredRooms()
		var resources roomResources
		for _, room := range rooms {
			clientStart := len(resources.clientObservations)
			room.mu.Lock()
			playerIDs, removed := resources.removeRoomLocked(room)
			clientTransitions := s.clientObservationTransitionsLocked(resources.clientObservations[clientStart:], -1)
			room.mu.Unlock()
			s.publishDisconnectedClients(clientTransitions)
			if removed && s.deleteRoomIfSame(room.ID, room) {
				s.releasePlayerIDs(playerIDs)
			}
		}
		resources.sessions = append(resources.sessions, activeSessions...)
		resources.close("store closed")
		waitClientSessions(resources.sessions)
		for _, lifecycleDone := range activeSessionDone {
			<-lifecycleDone
		}
	})
}

func waitClientSessions(sessions []*clientSession) {
	for _, session := range sessions {
		if session == nil {
			continue
		}
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
	for _, ticker := range r.tickers {
		ticker.Stop()
	}
	for _, stop := range r.stops {
		close(stop)
	}
	for _, session := range r.sessions {
		session.close(websocket.StatusNormalClosure, reason)
	}
}
