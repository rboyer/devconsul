package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-uuid"
	"github.com/rboyer/devconsul/cachestore"
)

const programName = "devconsul"

const (
	PrimaryDC   = "dc1"
	SecondaryDC = "dc2"
)

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

	tool := &Tool{
		logger: logger,
	}
	if err := tool.Init(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	var err error
	switch subcommand {
	case "config":
		err = tool.commandConfig()
	case "gen":
		err = tool.commandGen()
	case "boot":
		err = tool.commandBoot()
	default:
		logger.Error("Unknown subcommand", "subcommand", subcommand)
		os.Exit(1)
	}

	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	os.Exit(0)
}

type Tool struct {
	logger hclog.Logger

	cache         *cachestore.Store
	config        *Config
	runtimeConfig RuntimeConfig

	topology *Topology // for boot/gen

	masterToken         string      // for boot
	clientDC1           *api.Client // for boot
	clientDC2           *api.Client // for boot
	replicationSecretID string      // for boot

	tokens map[string]string
}

type RuntimeConfig struct {
	GossipKey        string
	AgentMasterToken string
}

func (t *Tool) Init() error {
	// this needs to run from the same directory as the config.hcl file
	// for the project
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(cwd, "config.hcl")); err != nil {
		return fmt.Errorf("This must be run from the home of the checkout: %v", err)
	}

	cacheDir := filepath.Join(cwd, "cache")

	t.cache, err = cachestore.New(cacheDir)
	if err != nil {
		return err
	}

	t.config, err = LoadConfig()
	if err != nil {
		return err
	}

	// t.logger.Info("File config:\n" + jsonPretty(t.config))

	if t.config.Encryption.Gossip {
		if err := t.initGossipKey(); err != nil {
			return err
		}
	}

	if err := t.initAgentMasterToken(); err != nil {
		return err
	}

	// t.logger.Info("Runtime config:\n" + jsonPretty(&t.runtimeConfig))

	return nil
}

func (t *Tool) initAgentMasterToken() error {
	agentMasterToken, err := t.cache.LoadValue("agent-master-token")
	if err != nil {
		return err
	}
	if agentMasterToken != "" {
		t.runtimeConfig.AgentMasterToken = agentMasterToken
		return nil
	}

	agentMasterToken, err = uuid.GenerateUUID()
	if err != nil {
		return err
	}

	if err := t.cache.SaveValue("agent-master-token", agentMasterToken); err != nil {
		return err
	}
	t.runtimeConfig.AgentMasterToken = agentMasterToken

	return nil
}

func (t *Tool) initGossipKey() error {
	gossipKey, err := t.cache.LoadValue("gossip-key")
	if err != nil {
		return err
	}
	if gossipKey != "" {
		t.runtimeConfig.GossipKey = gossipKey
		return nil
	}

	key := make([]byte, 16)
	n, err := rand.Reader.Read(key)
	if err != nil {
		return fmt.Errorf("Error reading random data: %s", err)
	}
	if n != 16 {
		return fmt.Errorf("Couldn't read enough entropy. Generate more entropy!")
	}

	gossipKey = base64.StdEncoding.EncodeToString(key)

	if err := t.cache.SaveValue("gossip-key", gossipKey); err != nil {
		return err
	}

	t.runtimeConfig.GossipKey = gossipKey

	return nil
}

func (t *Tool) setToken(typ, k, v string) {
	if t.tokens == nil {
		t.tokens = make(map[string]string)
	}
	t.tokens[typ+"/"+k] = v
}

func (t *Tool) getToken(typ, k string) string {
	if t.tokens == nil {
		return ""
	}
	return t.tokens[typ+"/"+k]
}

func (t *Tool) mustGetToken(typ, k string) string {
	tok := t.getToken(typ, k)
	if tok == "" {
		panic("token for '" + typ + "/" + k + "' not set:" + jsonPretty(t.tokens))
	}
	return tok
}
