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
	logger hclog.Logger

	cache   *cachestore.Store
	config2 *FlatConfig

	topology *Topology // for boot/gen
}

func (c *Core) Init() error {
	// this needs to run from the same directory as the config.hcl file
	// for the project
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(cwd, "config.hcl")); err != nil {
		return fmt.Errorf("Missing required config.hcl file", err)
	}

	cacheDir := filepath.Join(cwd, "cache")

	c.cache, err = cachestore.New(cacheDir)
	if err != nil {
		return err
	}

	c.config2, c.topology, err = LoadConfig()
	if err != nil {
		return err
	}

	// t.logger.Info("File config:\n" + jsonPretty(t.config))

	if c.config2.EncryptionGossip {
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

func (c *Core) initConsulImage() error {
	if !strings.HasSuffix(c.config2.ConsulImage, ":latest") {
		return nil
	}

	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return err
	}

	var errWriter bytes.Buffer
	var outWriter bytes.Buffer

	cmd := exec.Command(dockerBin, "image", "inspect", c.config2.ConsulImage)
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

	c.config2.ConsulImage = strings.TrimPrefix(obj[0].Id, "sha256:")
	return nil
}

func (c *Core) initAgentMasterToken() error {
	var err error
	c.config2.AgentMasterToken, err = c.cache.LoadOrSaveValue("agent-master-token", func() (string, error) {
		return uuid.GenerateUUID()
	})
	return err
}

func (c *Core) initGossipKey() error {
	var err error
	c.config2.GossipKey, err = c.cache.LoadOrSaveValue("gossip-key", func() (string, error) {
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
