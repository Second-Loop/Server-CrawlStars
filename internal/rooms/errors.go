package rooms

import "errors"

var (
	ErrRoomNotFound           = errors.New("room not found")
	ErrPlayerNotFound         = errors.New("player not found")
	ErrPlayerAlreadyConnected = errors.New("player already connected")
	ErrRoomFull               = errors.New("room full")
	ErrRoomHasNoPlayers       = errors.New("room has no players")
	ErrActiveRoomCapReached   = errors.New("active room cap reached")
	ErrInternal               = errors.New("internal server error")
)
