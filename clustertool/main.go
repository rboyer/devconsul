package main

import (
	"flag"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/hashicorp/go-hclog"
)

const programName = "clustertool"

type Command interface {
	RegisterFlags()
	Run(hclog.Logger) error
}

var allCommands = make(map[string]Command)

func main() {
	log.SetOutput(io.Discard)

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

	if subcommand == "help" {
		var keys []string
		for name := range allCommands {
			keys = append(keys, name)
		}
		sort.Strings(keys)

		logger.Info("available commands: [" + strings.Join(keys, ", ") + "]")
		os.Exit(0)
	}

	for name, cmd := range allCommands {
		if name == subcommand {
			cmd.RegisterFlags()
			flag.Parse()

			if err := cmd.Run(logger); err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
			os.Exit(0)
			return
		}
	}

	logger.Error("Unknown subcommand", "subcommand", subcommand)
	os.Exit(1)
}
