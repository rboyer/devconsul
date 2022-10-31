package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-hclog"

	"github.com/rboyer/devconsul/app/runner"
	"github.com/rboyer/devconsul/cachestore"
	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

const (
	ProgramName       = "devconsul"
	DefaultConfigFile = "config.hcl"
)

// Deprecated: ensure rename
type Core = App

type App struct {
	logger  hclog.Logger
	rootDir string

	config   *config.Config
	topology *infra.Topology
	cache    *cachestore.Store
	runner   *runner.Runner

	BootInfo // for boot
}

func New(logger hclog.Logger) (*App, error) {
	c := &App{
		logger: logger,
	}

	// this needs to run from the same directory as the config.hcl file
	// for the project
	var err error
	c.rootDir, err = os.Getwd()
	if err != nil {
		return nil, err
	}

	_, err = os.Stat(filepath.Join(c.rootDir, DefaultConfigFile))
	if err != nil {
		return nil, fmt.Errorf("Missing required %s file: %v", DefaultConfigFile, err)
	}

	c.config, err = config.LoadConfig(DefaultConfigFile)
	if err != nil {
		return nil, err
	}

	c.topology, err = infra.CompileTopology(c.config)
	if err != nil {
		return nil, err
	}

	c.cache = &cachestore.Store{
		Dir: filepath.Join(c.rootDir, "cache"),
	}

	c.runner, err = runner.Load(logger, c.config.KubernetesEnabled)
	if err != nil {
		return nil, err
	}

	if c.config.EnterpriseEnabled && !c.runner.IsEnterprise() {
		return nil, errors.New("config requests enterprise but the binary is OSS")
	}

	return c, nil
}
