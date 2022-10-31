package main

import (
	"flag"
	"io"
	"log"
	"os"
	"strings"

	"github.com/hashicorp/go-hclog"

	"github.com/rboyer/devconsul/app"
)

type command struct {
	Name    string
	Func    func(*app.App) error
	Aliases []string
}

var allCommands = []command{
	{"up", (*app.App).RunBringUp, nil},                           // porcelain
	{"down", (*app.App).RunBringDown, []string{"destroy", "rm"}}, // porcelain
	{"restart", (*app.App).RunRestart, nil},                      // porcelain
	{"config", (*app.App).RunConfigDump, nil},                    // porcelain
	// ================ special scenarios
	{"force-docker", (*app.App).RunForceDocker, []string{"docker"}},
	{"primary", (*app.App).RunBringUpPrimary, []string{"up-primary", "up-pri"}},
	{"stop-dc2", (*app.App).RunStopDC2, nil},
	{"restart-dc2", (*app.App).RunRestartDC2, nil},
	{"save-grafana", (*app.App).RunDebugSaveGrafana, nil},
	{"config-entries", (*app.App).RunDebugListConfigs, nil},
	{"grpc-check", (*app.App).RunGRPCCheck, nil},
	{"check-mesh", (*app.App).RunCheckMesh, nil},
}

func main() {
	log.SetOutput(io.Discard)

	// Create logger
	logger := hclog.New(&hclog.LoggerOptions{
		Name:       app.ProgramName,
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
	os.Args[0] = app.ProgramName

	var resetOnce bool
	flag.BoolVar(&resetOnce, "force", false, "force one time operations to run again")
	flag.Parse()

	if resetOnce {
		if err := app.ResetRunOnceMemory(); err != nil {
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

	core, err := app.New(logger)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	commandMap := make(map[string]func(core *app.App) error)
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
