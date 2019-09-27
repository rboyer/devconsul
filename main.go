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

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-uuid"
	"github.com/rboyer/devconsul/cachestore"
)

const programName = "devconsul"

const (
	PrimaryDC   = "dc1"
	SecondaryDC = "dc2"
	TertiaryDC  = "dc3"
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
	clientDC3           *api.Client // for boot
	replicationSecretID string      // for boot

	tokens map[string]string
}

func (t *Tool) clientForDC(dc string) *api.Client {
	switch dc {
	case PrimaryDC:
		return t.clientDC1
	case SecondaryDC:
		return t.clientDC2
	case TertiaryDC:
		return t.clientDC3
	default:
		panic("illegal dc name '" + dc + "'")
	}
}

type RuntimeConfig struct {
	GossipKey        string
	AgentMasterToken string
	ConsulImage      string // this may be a resolved value if :latest were used
}

func (t *Tool) Init() error {
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

	if err := t.initConsulImage(); err != nil {
		return err
	}

	// t.logger.Info("Runtime config:\n" + jsonPretty(&t.runtimeConfig))
	// $(docker image inspect consul-dev:latest | jq -r '.[0].Id' | cut -d':' -f2-)

	return nil
}

func (t *Tool) initConsulImage() error {
	if !strings.HasSuffix(t.config.ConsulImage, ":latest") {
		t.runtimeConfig.ConsulImage = t.config.ConsulImage
		return nil
	}

	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return err
	}

	var errWriter bytes.Buffer
	var outWriter bytes.Buffer

	cmd := exec.Command(dockerBin, "image", "inspect", t.config.ConsulImage)
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

	t.runtimeConfig.ConsulImage = strings.TrimPrefix(obj[0].Id, "sha256:")
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
