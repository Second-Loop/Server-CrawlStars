package rooms

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"io"
)

const (
	roomIDRandomBytes   = 16
	playerIDRandomBytes = 16
	sessionRandomBytes  = 32
	identityRetryLimit  = 8
	roomIDPrefix        = "room_"
	playerIDPrefix      = "player_"
)

type playerSession struct {
	digest [sha256.Size]byte
}

// authenticatePlayer requires r.mu because sessions is room-owned state.
func (r *room) authenticatePlayer(playerID string, rawToken string) bool {
	session, ok := r.sessions[playerID]
	if !ok {
		return false
	}
	digest := sha256.Sum256([]byte(rawToken))
	return subtle.ConstantTimeCompare(session.digest[:], digest[:]) == 1
}

func randomValue(reader io.Reader, prefix string, size int) (string, error) {
	random := make([]byte, size)
	if _, err := io.ReadFull(reader, random); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(random), nil
}
