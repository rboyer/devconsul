package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

func (a *App) maybeInitGossipKey() error {
	if !a.config.EncryptionGossip {
		return a.cache.DelValue("gossip-key")
	}

	var err error
	a.config.GossipKey, err = a.cache.LoadOrSaveValue("gossip-key", func() (string, error) {
		key := make([]byte, 16)
		n, err := rand.Reader.Read(key)
		if err != nil {
			return "", fmt.Errorf("Error reading random data: %s", err)
		}
		if n != 16 {
			return "", fmt.Errorf("Couldn't read enough entropy. Generate more entropy!")
		}

		return base64.StdEncoding.EncodeToString(key), nil
	})
	return err
}
