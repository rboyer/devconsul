package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-uuid"
	"github.com/rboyer/safeio"

	"github.com/rboyer/devconsul/cachestore"
	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

const (
	programName       = "devconsul"
	defaultConfigFile = "config.hcl"
)

type command struct {
	Name    string
	Func    func(*Core) error
	Aliases []string
}

var allCommands = []command{
	{"up", (*Core).RunBringUp, nil},                           // porcelain
	{"down", (*Core).RunBringDown, []string{"destroy", "rm"}}, // porcelain
	{"restart", (*Core).RunRestart, nil},                      // porcelain
	{"config", (*Core).RunConfigDump, nil},                    // porcelain
	// ================ special scenarios
	{"force-docker", (*Core).RunForceDocker, []string{"docker"}},
	{"primary", (*Core).RunBringUpPrimary, []string{"up-primary", "up-pri"}},
	{"stop-dc2", (*Core).RunStopDC2, nil},
	{"restart-dc2", (*Core).RunRestartDC2, nil},
	{"save-grafana", (*Core).RunDebugSaveGrafana, nil},
	{"config-entries", (*Core).RunDebugListConfigs, nil},
	{"grpc-check", (*Core).RunGRPCCheck, nil},
}

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
		logger.Error("Missing required subcommand")
		os.Exit(1)
	}
	subcommand := os.Args[1]
	os.Args = os.Args[1:]
	os.Args[0] = programName

	var resetOnce bool
	flag.BoolVar(&resetOnce, "force", false, "force one time operations to run again")
	flag.Parse()

	if resetOnce {
		if err := resetRunOnceMemory(); err != nil {
			logger.Error(err.Error())
			os.Exit(1)
		}
	}

	if subcommand == "help" {
		var keys []string
		for _, cmd := range allCommands {
			keys = append(keys, cmd.Name)
		}

		logger.Info("available commands: [" + strings.Join(keys, ", ") + "]")
		os.Exit(0)
	}

	destroying := (subcommand == "down")
	configOnly := (subcommand == "config")

	core, err := NewCore(logger, configOnly, destroying)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	commandMap := make(map[string]func(core *Core) error)
	for _, cmd := range allCommands {
		commandMap[cmd.Name] = cmd.Func
		for _, alt := range cmd.Aliases {
			commandMap[alt] = cmd.Func
		}
	}

	runFn, ok := commandMap[subcommand]
	if !ok {
		logger.Error("Unknown subcommand", "subcommand", subcommand)
		os.Exit(1)
	}

	err = runFn(core)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	os.Exit(0)
}

type Core struct {
	logger  hclog.Logger
	rootDir string

	devconsulBin string // special

	consulBin   string
	tfBin       string
	dockerBin   string
	minikubeBin string // optional
	kubectlBin  string // optional

	cache  *cachestore.Store
	config *config.Config

	topology *infra.Topology

	BootInfo // for boot
}

func NewCore(logger hclog.Logger, configOnly, destroying bool) (*Core, error) {
	c := &Core{
		logger: logger,
	}

	// this needs to run from the same directory as the config.hcl file
	// for the project
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	c.rootDir = cwd

	if _, err := os.Stat(filepath.Join(c.rootDir, defaultConfigFile)); err != nil {
		return nil, fmt.Errorf("Missing required %s file: %v", defaultConfigFile, err)
	}

	cfg, err := config.LoadConfig(defaultConfigFile)
	if err != nil {
		return nil, err
	}

	topology, err := infra.CompileTopology(cfg)
	if err != nil {
		return nil, err
	}

	c.config = cfg
	c.topology = topology

	if configOnly {
		return c, nil
	}

	if err := c.lookupBinaries(); err != nil {
		return nil, err
	}

	if destroying {
		return c, nil
	}

	cacheDir := filepath.Join(c.rootDir, "cache")

	c.cache, err = cachestore.New(cacheDir)
	if err != nil {
		return nil, err
	}

	if c.config.EncryptionTLS {
		if err := c.initTLS(); err != nil {
			return nil, err
		}
	} else {
		if err := os.RemoveAll("cache/tls"); err != nil {
			return nil, err
		}
	}

	if c.config.EncryptionGossip {
		if err := c.initGossipKey(); err != nil {
			return nil, err
		}
	} else {
		if err := c.cache.DelValue("gossip-key"); err != nil {
			return nil, err
		}
	}

	if c.config.SecurityDisableACLs {
		if err := c.cache.DelValue("agent-master-token"); err != nil {
			return nil, err
		}
	} else {
		if err := c.initAgentMasterToken(); err != nil {
			return nil, err
		}
	}

	return c, nil
}

func (c *Core) lookupBinaries() error {
	var err error

	c.devconsulBin, err = os.Executable()
	if err != nil {
		return err
	}

	type item struct {
		name string
		dest *string
		warn string // optional
	}
	lookup := []item{
		{"consul", &c.consulBin, "run 'make dev' from your consul checkout"},
		{"docker", &c.dockerBin, ""},
		{"terraform", &c.tfBin, ""},
	}
	if c.config.KubernetesEnabled {
		lookup = append(lookup,
			item{"minikube", &c.minikubeBin, ""},
			item{"kubectl", &c.kubectlBin, ""},
		)
	}

	var bins []string
	for _, i := range lookup {
		*i.dest, err = exec.LookPath(i.name)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				if i.warn != "" {
					return fmt.Errorf("Could not find %q on path (%s): %w", i.name, i.warn, err)
				} else {
					return fmt.Errorf("Could not find %q on path: %w", i.name, err)
				}
			}
			return fmt.Errorf("Unexpected failure looking for %q on path: %w", i.name, err)
		}
		bins = append(bins, *i.dest)
	}
	c.logger.Debug("using binaries", "paths", bins)

	return nil
}

func (c *Core) initTLS() error {
	cacheDir := filepath.Join(c.rootDir, "cache")
	tlsDir := filepath.Join(cacheDir, "tls")
	if err := os.MkdirAll(tlsDir, 0755); err != nil {
		return err
	}

	if exists, err := filesExist(tlsDir, "consul-agent-ca-key.pem", "consul-agent-ca.pem"); err != nil {
		return err
	} else if !exists {
		var errWriter bytes.Buffer

		cmd := exec.Command(c.consulBin, "tls", "ca", "create")
		cmd.Dir = tlsDir
		cmd.Stdout = nil
		cmd.Stderr = &errWriter
		cmd.Stdin = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("could not invoke 'consul' to create a CA: %v : %s", err, errWriter.String())
		}
		c.logger.Info("created cluster CA")
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

		c.logger.Info("creating certs", "prefix", prefix)

		var errWriter bytes.Buffer

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

		cmd := exec.Command(c.consulBin, args...)
		cmd.Dir = tlsDir
		cmd.Stdout = nil
		cmd.Stderr = &errWriter
		cmd.Stdin = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("could not invoke 'consul tls cert create': %v : %s", err, errWriter.String())
		}

		return nil
	}

	for _, cluster := range c.topology.Clusters() {
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

func addFileToHash(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func jsonPretty(val interface{}) string {
	out, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return "<ERROR>"
	}
	return string(out)
}

func cmdExec(name, binary string, args []string, stdout io.Writer) error {
	var errWriter bytes.Buffer

	if stdout == nil {
		stdout = os.Stdout // TODO: wrap logs
	}

	cmd := exec.Command(binary, args...)
	cmd.Stdout = stdout
	cmd.Stderr = &errWriter
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not invoke %q: %v : %s", name, err, errWriter.String())
	}

	return nil
}

func runOnce(name string, fn func() error) error {
	if ok, err := hasRunOnce("init"); err != nil {
		return err
	} else if ok {
		return nil
	}

	if err := fn(); err != nil {
		return err
	}

	_, err := safeio.WriteToFile(bytes.NewReader([]byte(name)), "cache/"+name+".done", 0644)
	return err
}

func hasRunOnce(name string) (bool, error) {
	b, err := ioutil.ReadFile("cache/" + name + ".done")
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return name == string(b), nil
}

func checkHasRunOnce(name string) error {
	ok, err := hasRunOnce(name)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return fmt.Errorf("'%s %s' has not yet been run", programName, name)
}

func resetRunOnceMemory() error {
	files, err := filepath.Glob("cache/*.done")
	if err != nil {
		return err
	}

	for _, fn := range files {
		err := os.Remove(fn)
		if !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}
