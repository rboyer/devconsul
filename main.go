package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-uuid"
	"github.com/rboyer/devconsul/cachestore"
)

const programName = "devconsul"
const PrimaryDC = "dc1"

func main() {
	log.SetOutput(ioutil.Discard)

	// Create logger
	logger := hclog.New(&hclog.LoggerOptions{
		Name:       programName,
		Level:      hclog.Debug,
		Output:     os.Stderr,
		JSONFormat: false,
	})

	if len(os.Args) == 1 {
		logger.Error("Missing required subcommand: [config, gen, boot]")
		os.Exit(1)
	}
	subcommand := os.Args[1]
	os.Args = os.Args[1:]
	os.Args[0] = programName

	core := &Core{
		logger: logger,
	}
	if err := core.Init(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	var runner interface {
		Run() error
	}
	switch subcommand {
	case "config":
		runner = &CommandConfig{Core: core}
	case "gen":
		runner = &CommandGenerate{Core: core}
	case "nomad":
		runner = &CommandNomad{Core: core}
	case "boot":
		runner = &CommandBoot{Core: core}
	default:
		logger.Error("Unknown subcommand", "subcommand", subcommand)
		os.Exit(1)
	}

	err := runner.Run()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	os.Exit(0)
}

type Core struct {
	logger  hclog.Logger
	rootDir string

	cache  *cachestore.Store
	config *FlatConfig

	topology *Topology // for boot/gen
}

func (c *Core) Init() error {
	// this needs to run from the same directory as the config.hcl file
	// for the project
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	c.rootDir = cwd

	if _, err := os.Stat(filepath.Join(c.rootDir, "config.hcl")); err != nil {
		return fmt.Errorf("Missing required config.hcl file: %v", err)
	}

	cacheDir := filepath.Join(c.rootDir, "cache")

	c.cache, err = cachestore.New(cacheDir)
	if err != nil {
		return err
	}

	c.config, c.topology, err = LoadConfig()
	if err != nil {
		return err
	}

	// t.logger.Info("File config:\n" + jsonPretty(t.config))
	if c.config.EncryptionTLS {
		if err := c.initTLS(); err != nil {
			return err
		}
	}

	if c.config.EncryptionGossip {
		if err := c.initGossipKey(); err != nil {
			return err
		}
	}

	if err := c.initAgentMasterToken(); err != nil {
		return err
	}

	if err := c.initConsulImage(); err != nil {
		return err
	}

	return nil
}

func (c *Core) initTLS() error {
	consulBin, err := exec.LookPath("consul")
	if err != nil {
		if execErr, ok := err.(*exec.Error); ok {
			if execErr == exec.ErrNotFound {
				return fmt.Errorf("no 'consul' binary on PATH. Please run 'make dev' from your consul checkout")
			}
		}
		return err
	}

	cacheDir := filepath.Join(c.rootDir, "cache")
	tlsDir := filepath.Join(cacheDir, "tls")
	if err := os.MkdirAll(tlsDir, 0755); err != nil {
		return err
	}

	if exists, err := filesExist(tlsDir, "consul-agent-ca-key.pem", "consul-agent-ca.pem"); err != nil {
		return err
	} else if !exists {
		var errWriter bytes.Buffer

		cmd := exec.Command(consulBin, "tls", "ca", "create")
		cmd.Dir = tlsDir
		cmd.Stdout = nil
		cmd.Stderr = &errWriter
		cmd.Stdin = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("could not invoke 'consul': %v : %s", err, errWriter.String())
		}
		c.logger.Info("created cluster CA")
	}

	genCert := func(dc *Datacenter, server bool, idx int) error {
		typ := "client"
		if server {
			typ = "server"
		}

		prefix := fmt.Sprintf("%s-%s-consul-%d", dc.Name, typ, idx)

		exists, err := filesExist(tlsDir, prefix+"-key.pem", prefix+".pem")
		if err != nil {
			return err
		} else if exists {
			return nil
		}

		c.logger.Info("creating certs", "prefix", prefix)

		var errWriter bytes.Buffer

		var args []string
		if server {
			nodeName := fmt.Sprintf("%s-server%d-pod", dc.Name, idx+1)
			args = []string{
				"tls", "cert", "create",
				"-server",
				"-dc=" + dc.Name,
				"-node=" + nodeName,
			}
		} else {
			args = []string{
				"tls", "cert", "create",
				"-client",
				"-dc=" + dc.Name,
			}
		}

		cmd := exec.Command(consulBin, args...)
		cmd.Dir = tlsDir
		cmd.Stdout = nil
		cmd.Stderr = &errWriter
		cmd.Stdin = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("could not invoke 'consul tls cert create': %v : %s", err, errWriter.String())
		}

		return nil
	}

	for _, dc := range c.topology.Datacenters() {
		for i := 0; i < dc.Servers; i++ {
			if err := genCert(&dc, true, i); err != nil {
				return err
			}
		}
		for i := 0; i < dc.Clients; i++ {
			if err := genCert(&dc, false, i); err != nil {
				return err
			}
		}
	}

	return nil
}

func filesExist(parent string, paths ...string) (bool, error) {
	for _, p := range paths {
		ok, err := fileExists(filepath.Join(parent, p))
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else {
		return true, nil
	}
}

func (c *Core) initConsulImage() error {
	if !strings.HasSuffix(c.config.ConsulImage, ":latest") {
		return nil
	}

	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return err
	}

	var errWriter bytes.Buffer
	var outWriter bytes.Buffer

	cmd := exec.Command(dockerBin, "image", "inspect", c.config.ConsulImage)
	cmd.Stdout = &outWriter
	cmd.Stderr = &errWriter
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not invoke 'docker': %v : %s", err, errWriter.String())
	}

	dec := json.NewDecoder(&outWriter)

	var obj []struct {
		Id string `json:"Id"`
	}
	if err := dec.Decode(&obj); err != nil {
		return err
	}

	if len(obj) != 1 {
		return fmt.Errorf("unexpected docker output")
	}

	if !strings.HasPrefix(obj[0].Id, "sha256:") {
		return fmt.Errorf("unexpected docker output: %q", obj[0].Id)
	}

	c.config.ConsulImage = strings.TrimPrefix(obj[0].Id, "sha256:")
	return nil
}

func (c *Core) initAgentMasterToken() error {
	var err error
	c.config.AgentMasterToken, err = c.cache.LoadOrSaveValue("agent-master-token", func() (string, error) {
		return uuid.GenerateUUID()
	})
	return err
}

func (c *Core) initGossipKey() error {
	var err error
	c.config.GossipKey, err = c.cache.LoadOrSaveValue("gossip-key", func() (string, error) {
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
