package rooms

import (
	"time"

	"nhooyr.io/websocket"
)

func (s *Store) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true

	var resources roomResources
	for _, room := range s.rooms {
		resources.add(room)
	}
	s.mu.Unlock()

	resources.close("store closed")
}

func (s *Store) cleanupExpired() {
	now := s.clock.Now()

	s.mu.Lock()
	var resources roomResources
	for id, room := range s.rooms {
		if !room.isExpired(now) {
			continue
		}
		delete(s.rooms, id)
		resources.add(room)
	}
	s.mu.Unlock()

	resources.close(defaultRoomWebSocketCloseMsg)
}

func (r *room) isExpired(now time.Time) bool {
	if !r.createdAt.IsZero() && !now.Before(r.createdAt.Add(defaultHardRoomLifetime)) {
		return true
	}
	if len(r.clients) > 0 {
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
	tickers []ticker
	stops   []chan struct{}
	conns   []*websocket.Conn
}

func (r *roomResources) add(room *room) {
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
	for _, conn := range room.clients {
		if conn != nil {
			r.conns = append(r.conns, conn)
		}
	}
	room.clients = nil
}

func (r roomResources) close(reason string) {
	for _, ticker := range r.tickers {
		ticker.Stop()
	}
	for _, stop := range r.stops {
		close(stop)
	}
	for _, conn := range r.conns {
		_ = conn.Close(websocket.StatusNormalClosure, reason)
	}
}
