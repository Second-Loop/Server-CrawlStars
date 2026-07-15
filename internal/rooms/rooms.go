package rooms

import "time"

type RoomStatus string

const (
	RoomStatusWaiting RoomStatus = "waiting"
	RoomStatusStarted RoomStatus = "started"
)

type MatchStatus string

const (
	MatchStatusMatched  MatchStatus = "matched"
	MatchStatusLoading  MatchStatus = "loading"
	MatchStatusStarting MatchStatus = "starting"
	MatchStatusStarted  MatchStatus = "started"
)

const (
	defaultWaitingRoomIdleTTL    = 10 * time.Minute
	defaultDisconnectedRoomTTL   = 5 * time.Minute
	defaultHardRoomLifetime      = time.Hour
	defaultRoomWebSocketCloseMsg = "room expired"
	defaultRoomDebugDeleteMsg    = "room deleted"
	defaultMatchCancelMsg        = "match canceled"
	defaultGameEndCloseMsg       = "game ended"
	webSocketWriteTimeout        = 10 * time.Millisecond
	matchCountdownSeconds        = 5
)

type clock interface {
	Now() time.Time
	NewTicker(duration time.Duration) ticker
}

type ticker interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

type realTicker struct {
	*time.Ticker
}

func (realClock) NewTicker(duration time.Duration) ticker {
	return realTicker{Ticker: time.NewTicker(duration)}
}

func (realClock) Now() time.Time {
	return time.Now()
}

func (t realTicker) C() <-chan time.Time {
	return t.Ticker.C
}
