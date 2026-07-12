package rooms

import (
	"crypto/sha256"
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

func randomValue(reader io.Reader, prefix string, size int) (string, error) {
	random := make([]byte, size)
	if _, err := io.ReadFull(reader, random); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(random), nil
}
