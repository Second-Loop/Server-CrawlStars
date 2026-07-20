package rooms

import "errors"

var (
	ErrRoomNotFound           = errors.New("room not found")
	ErrPlayerNotFound         = errors.New("player not found")
	ErrPlayerAlreadyConnected = errors.New("player already connected")
	ErrUnauthorized           = errors.New("unauthorized")
	ErrRoomFull               = errors.New("room full")
	ErrRoomHasNoPlayers       = errors.New("room has no players")
	ErrActiveRoomCapReached   = errors.New("active room cap reached")
	ErrInvalidRequest         = errors.New("invalid request")
	ErrInvalidGameMode        = errors.New("invalid game mode")
	ErrInvalidCharacterType   = errors.New("invalid character type")
	ErrInternal               = errors.New("internal server error")
)
