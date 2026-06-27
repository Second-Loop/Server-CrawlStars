package clientconfig

import (
	"bytes"
	"io"

	_ "embed"
)

//go:embed game-config.json
var defaultGameConfig []byte

func Reader() io.Reader {
	return bytes.NewReader(defaultGameConfig)
}
