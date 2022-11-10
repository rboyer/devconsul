package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/go-cleanhttp"
	vaultapi "github.com/hashicorp/vault/api"
)

func (c *Core) initVault() error {
	if !c.config.VaultEnabled {
		if err := c.cache.DelValue("vault-unseal-key"); err != nil {
			return err
		}
		if err := c.cache.DelValue("vault-token"); err != nil {
			return err
		}
		c.vault = nil
		c.vaultUnsealKey = ""
		c.vaultToken = ""
		return nil
	}

	cfg := vaultapi.DefaultConfig()
	cfg.Address = "http://10.0.100.111:8200"
	cfg.Logger = c.logger.Named("vault")
	cfg.HttpClient = cleanhttp.DefaultPooledClient()

	var err error
	c.vault, err = vaultapi.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("error creating vault api client: %w", err)
	}
	c.logger.Info("Vault client created", "addr", cfg.Address)

	c.vaultUnsealKey, err = c.cache.LoadValue("vault-unseal-key")
	if err != nil {
		return err
	}

	c.vaultToken, err = c.cache.LoadValue("vault-token")
	if err != nil {
		return err
	}

	// Check where we're at.
CHECK_STATUS:
	status, err := c.vault.Sys().SealStatus()
	if err != nil {
		c.logger.Warn("error checking seal status; waiting for vault to start", "error", err)
		time.Sleep(250 * time.Millisecond)
		goto CHECK_STATUS
	}
	c.logger.Info("Vault current status", "init", status.Initialized, "sealed", status.Sealed)

	if !status.Initialized {
		resp, err := c.vault.Sys().Init(&vaultapi.InitRequest{
			SecretShares:    1,
			SecretThreshold: 1,
		})
		if err != nil {
			return fmt.Errorf("error initializing vault: %w", err)
		}
		c.vaultToken = resp.RootToken
		c.vaultUnsealKey = resp.KeysB64[0]

		if err := c.cache.SaveValue("vault-unseal-key", c.vaultUnsealKey); err != nil {
			return err
		}

		if err := c.cache.SaveValue("vault-token", c.vaultToken); err != nil {
			return err
		}
	}
	if c.vaultUnsealKey == "" {
		return fmt.Errorf("no memory of vault unseal key; destroy and recreate vault")
	}
	if c.vaultToken == "" {
		return fmt.Errorf("no memory of vault root token; destroy and recreate vault")
	}

	if status.Sealed {
		if _, err = c.vault.Sys().Unseal(c.vaultUnsealKey); err != nil {
			return fmt.Errorf("error unsealing vault: %w", err)
		}

		goto CHECK_STATUS
	}

	c.logger.Info("vault root token", "token", c.vaultToken)
	c.vault.SetToken(c.vaultToken)

	// poke it
	return c.pokeVault()
}

func (c *Core) pokeVault() error {
	// Enable the KV secrets engine.

	if exists, err := mountExists(c.vault, "secret"); err != nil {
		return fmt.Errorf("error checking existing kv secrets mount: %w", err)
	} else if !exists {
		mountInput := &vaultapi.MountInput{
			Type: "kv",
			Options: map[string]string{
				"version": "2",
			},
		}

		if err := c.vault.Sys().Mount("secret/", mountInput); err != nil {
			return fmt.Errorf("error enabling kv secrets engine: %w", err)
		}
	}

	data := map[string]any{
		"aaa": "bbb",
	}

	return writeVaultSecret(c.vault, "test", data)
}

func writeVaultSecret(vault *vaultapi.Client, key string, data map[string]any) error {
	_, err := vault.Logical().Write("secret/data/"+key, map[string]any{"data": data})
	return err
}

func mountExists(vault *vaultapi.Client, path string) (bool, error) {
	_, err := vault.Sys().MountConfig(path)
	if err != nil {
		if strings.Contains(err.Error(), "cannot fetch sysview for path") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
