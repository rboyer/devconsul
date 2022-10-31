package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rboyer/devconsul/infra"
)

func (a *App) maybeInitTLS() error {
	if !a.config.EncryptionTLS {
		return os.RemoveAll("cache/tls")
	}

	cacheDir := filepath.Join(a.rootDir, "cache")
	tlsDir := filepath.Join(cacheDir, "tls")
	if err := os.MkdirAll(tlsDir, 0755); err != nil {
		return err
	}

	if exists, err := filesExist(tlsDir, "consul-agent-ca-key.pem", "consul-agent-ca.pem"); err != nil {
		return err
	} else if !exists {
		if err := a.runner.ConsulExec([]string{"tls", "ca", "create"}, nil, tlsDir); err != nil {
			return fmt.Errorf("error creating CA: %w", err)
		}
		a.logger.Info("created cluster CA")
	}

	// TODO: peering
	genCert := func(cluster *infra.Cluster, server bool, idx int) error {
		typ := "client"
		if server {
			typ = "server"
		}

		prefix := fmt.Sprintf("%s-%s-consul-%d", cluster.Name, typ, idx)

		exists, err := filesExist(tlsDir, prefix+"-key.pem", prefix+".pem")
		if err != nil {
			return err
		} else if exists {
			return nil
		}

		a.logger.Info("creating certs", "prefix", prefix)

		var args []string
		if server {
			nodeName := fmt.Sprintf("%s-server%d-pod", cluster.Name, idx+1)
			args = []string{
				"tls", "cert", "create",
				"-server",
				"-dc=" + cluster.Name,
				"-node=" + nodeName,
			}
		} else {
			args = []string{
				"tls", "cert", "create",
				"-client",
				"-dc=" + cluster.Name,
			}
		}

		if err := a.runner.ConsulExec(args, nil, tlsDir); err != nil {
			return fmt.Errorf("error creating agent certificates: %w", err)
		}

		return nil
	}

	for _, cluster := range a.topology.Clusters() {
		for i := 0; i < cluster.Servers; i++ {
			if err := genCert(&cluster, true, i); err != nil {
				return err
			}
		}
		for i := 0; i < cluster.Clients; i++ {
			if err := genCert(&cluster, false, i); err != nil {
				return err
			}
		}
	}

	return nil
}
