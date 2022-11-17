package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-cleanhttp"
	vaultapi "github.com/hashicorp/vault/api"
	"github.com/mitchellh/copystructure"
)

const VaultAddr = "http://10.0.100.111:8200"

func (c *Core) initVault() error {
	c.vaultCATokens = make(map[string]string)
	if !c.config.VaultEnabled {
		if err := c.cache.DelValue("vault-unseal-key"); err != nil {
			return err
		}
		if err := c.cache.DelValue("vault-token"); err != nil {
			return err
		}
		if _, err := c.cache.DelValuePrefix("vault-token-ca-"); err != nil {
			return err
		}
		c.vault = nil
		c.vaultUnsealKey = ""
		c.vaultToken = ""
		return nil
	}

	cfg := vaultapi.DefaultConfig()
	cfg.Address = VaultAddr
	// cfg.Logger = c.logger.Named("vault")
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

func (c *Core) maybeInitVaultForMeshCA(cluster string) error {
	if _, ok := c.config.VaultAsMeshCA[cluster]; !ok {
		// switch it back
		return c.ensureCAUsesConsul(cluster)
	}

	if err := c.loadOrCreateVaultTokenForCA(cluster); err != nil {
		return fmt.Errorf("error creating vault token for ca: %w", err)
	}

	return c.ensureCAUsesVault(cluster)
}

func (c *Core) loadOrCreateVaultTokenForCA(cluster string) error {
	rootPath := "connect_root__" + cluster
	imPath := "connect_inter__" + cluster

	vaultToken, err := c.cache.LoadValue("vault-token-ca-" + cluster)
	if err != nil {
		return err
	} else if vaultToken != "" {
		c.vaultCATokens[cluster] = vaultToken
		return nil
	}

	// Consul Managed PKI Mounts
	policyBody := fmt.Sprintf(`
path "/sys/mounts" {
  capabilities = [ "read" ]
}

path "/sys/mounts/%[1]s" {
  capabilities = [ "create", "read", "update", "delete", "list" ]
}

path "/sys/mounts/%[2]s" {
  capabilities = [ "create", "read", "update", "delete", "list" ]
}

# Needed for Consul 1.11+
path "/sys/mounts/%[2]s/tune" {
  capabilities = [ "update" ]
}

# TODO(rb): not present in docs; needed for provider swap FROM vault
# /v1/connect_root__dc1/root/sign-self-issued
path "%[1]s/root/sign-self-issued" {
  capabilities = [ "sudo", "update" ]
}

path "/%[1]s/*" {
  capabilities = [ "create", "read", "update", "delete", "list" ]
}

path "/%[2]s/*" {
  capabilities = [ "create", "read", "update", "delete", "list" ]
}
`, rootPath, imPath)

	c.vaultCATokens[cluster], err = c.createVaultTokenAndPolicy(
		"vault-token-ca-"+cluster,
		"vault-ca-"+cluster,
		policyBody,
		map[string]string{
			"cluster": cluster,
			"purpose": "ca",
		},
	)
	if err != nil {
		return fmt.Errorf("error creating vault token for CA management in %q: %w", cluster, err)
	}
	c.logger.Info("created vault token for mesh integration", "cluster", cluster, "token", c.vaultCATokens[cluster])

	return nil
}

func (c *Core) createVaultTokenAndPolicy(cacheName, policyName, policyBody string, tokenMeta map[string]string) (string, error) {
	if err := c.vault.Sys().PutPolicy(policyName, policyBody); err != nil {
		return "", fmt.Errorf("error creating vault policy %q: %w", policyName, err)
	}

	renew := true
	tok, err := c.vault.Auth().Token().Create(&vaultapi.TokenCreateRequest{
		Policies:  []string{policyName},
		Metadata:  tokenMeta,
		Renewable: &renew,
	})
	if err != nil {
		return "", fmt.Errorf("error creating vault token: %w", err)
	}

	if err := c.cache.SaveValue(cacheName, tok.Auth.ClientToken); err != nil {
		return "", err
	}

	return tok.Auth.ClientToken, nil
}

func (c *Core) ensureCAUsesConsul(cluster string) error {
	// TODO: clean up old vault PKI stuff

	update := &api.CAConfig{
		Provider: "consul",
		Config: map[string]any{
			"LeafCertTTL":         "72h",
			"IntermediateCertTTL": "8760h",
			"RotationPeriod":      "2160h",
			"PrivateKeyType":      "rsa",
			"PrivateKeyBits":      float64(2048),
		},
	}

	return c.ensureCAProvider(cluster, update, nil)
}

func (c *Core) ensureCAUsesVault(cluster string) error {
	vaultToken := c.vaultCATokens[cluster]
	if vaultToken == "" {
		return errors.New("programmer error: missing vault token")
	}

	update := &api.CAConfig{
		Provider: "vault",
		Config: map[string]any{
			"Address":             VaultAddr,
			"Token":               vaultToken,
			"RootPKIPath":         "connect_root__" + cluster,
			"IntermediatePKIPath": "connect_inter__" + cluster,
			//
			"LeafCertTTL":         "72h",
			"RotationPeriod":      "2160h",
			"IntermediateCertTTL": "8760h",
			"PrivateKeyType":      "rsa",
			"PrivateKeyBits":      float64(2048),
		},
	}

	return c.ensureCAProvider(cluster, update, []string{"Token"})
}

func copyConfig(c map[string]any) (map[string]any, error) {
	c2, err := copystructure.Copy(c)
	if err != nil {
		return nil, err
	}
	return c2.(map[string]any), nil
}

func (c *Core) ensureCAProvider(cluster string, update *api.CAConfig, simpleUpdateFields []string) error {
	var (
		client = c.clientForCluster(cluster)
		logger = c.logger.With("cluster", cluster)
	)

	curr, _, err := client.Connect().CAGetConfig(nil)
	if err != nil {
		return err
	}

	simpleUpdate := false
	if curr.Provider == update.Provider {
		currConfig, err := copyConfig(curr.Config)
		if err != nil {
			return err
		}
		updateConfig, err := copyConfig(update.Config)
		if err != nil {
			return err
		}

		for _, field := range simpleUpdateFields {
			if cmp.Diff(currConfig[field], updateConfig[field]) != "" {
				simpleUpdate = true
				delete(currConfig, field)
				delete(updateConfig, field)
			}
		}

		if diff := cmp.Diff(currConfig, updateConfig); diff != "" {
			return fmt.Errorf("current CA is configured as %q but configuration differs: %s", update.Provider, diff)
		}

		if !simpleUpdate {
			return nil
		}
	}

	_, err = client.Connect().CASetConfig(update, nil)
	if err != nil {
		return err
	}

	logger.Info("changed connect CA", "provider", update.Provider, "simpleUpdate", simpleUpdate)

	return err
}
