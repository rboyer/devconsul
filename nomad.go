package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/rboyer/safeio"
)

type CommandNomad struct {
	*Core

	verbose bool
	destroy bool
}

func (c *CommandNomad) Run() error {

	var tf bool
	flag.BoolVar(&tf, "tf", false, "")

	flag.BoolVar(&c.verbose, "v", false, "verbose")
	flag.BoolVar(&c.destroy, "rm", false, "destroy")
	flag.Parse()

	if tf {
		if err := c.syncDockerNetworksTF(); err != nil {
			return err
		}
		return nil
	}

	if err := c.syncDockerNetworks(); err != nil {
		return err
	}

	return nil
}

func (c *CommandNomad) syncDockerNetworksTF() error {
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		return err
	}

	if _, err = os.Stat(".terraform"); err != nil {
		if !os.IsNotExist(err) {
			return err
		}

		var errWriter bytes.Buffer
		var outWriter bytes.Buffer

		// tf init
		cmd := exec.Command(tfBin, "init")
		cmd.Stdout = &outWriter
		cmd.Stderr = &errWriter
		cmd.Stdin = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("could not invoke 'terraform': %v : %s", err, errWriter.String())
		}
	}

	var buf bytes.Buffer
	for _, net := range c.topology.Networks() {
		name := "nomad-" + net.Name
		buf.WriteString(fmt.Sprintf(`
resource "docker_network" %q {
  name       = %q
  attachable = true
  ipam_config {
    subnet = %q
  }
  labels {
    label = "devconsul"
    value = "1"
  }
}

`, name, name, net.CIDR,
		))
	}

	// tf apply -auto-approve

	// Ensure it looks tidy
	out := hclwrite.Format(bytes.TrimSpace(buf.Bytes()))

	_, err = safeio.WriteToFile(bytes.NewBuffer(out), "network.tf", 0644)
	if err != nil {
		return err
	}

	c.logger.Info("syncing networks with terraform")
	{
		// TODO: WRAP LOGS
		var errWriter bytes.Buffer
		// var outWriter bytes.Buffer

		// tf init
		cmd := exec.Command(tfBin, "apply", "-auto-approve")
		// cmd.Stdout = &outWriter
		cmd.Stdout = os.Stdout
		cmd.Stderr = &errWriter
		cmd.Stdin = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("could not invoke 'terraform': %v : %s", err, errWriter.String())
		}
	}

	return nil
}

func (c *CommandNomad) syncDockerNetworks() error {
	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return err
	}

	currentNetworks, err := dockerNetworkList(dockerBin)
	if err != nil {
		return err
	}

	var (
		skipDeleteNetworks = make(map[string]struct{})
		createNetworks     = make(map[string]struct{})
	)
	if !c.destroy {
		for _, net := range c.topology.Networks() {
			exists, err := dockerNetworkExists(dockerBin, "nomad-"+net.Name)
			if err != nil {
				return err
			}
			if exists {
				subnet, err := dockerNetworkInspectCIDR(dockerBin, "nomad-"+net.Name)
				if err != nil {
					return err
				}
				if subnet == net.CIDR {
					skipDeleteNetworks["nomad-"+net.Name] = struct{}{}
				} else {
					createNetworks["nomad-"+net.Name] = struct{}{}
				}
			} else {
				createNetworks["nomad-"+net.Name] = struct{}{}
			}
		}
	}

	// Process deletes first, in case we overlap
	for _, net := range currentNetworks {
		_, skip := skipDeleteNetworks[net]
		if c.destroy || !skip {
			if err := dockerNetworkDelete(dockerBin, net); err != nil {
				return err
			}

			c.logger.Info("network deleted", "network", net)
		}
	}

	if !c.destroy {
		// Create networks
		for _, net := range c.topology.Networks() {
			if _, ok := createNetworks["nomad-"+net.Name]; !ok {
				if c.verbose {
					c.logger.Debug("network already exists", "network", "nomad-"+net.Name)
				}
				continue
			}

			var errWriter bytes.Buffer
			var outWriter bytes.Buffer

			cmd := exec.Command(
				dockerBin, "network", "create",
				"--scope", "local",
				"--subnet", net.CIDR,
				"--attachable",
				"--label", "devconsul=1",
				"nomad-"+net.Name,
			)
			cmd.Stdout = &outWriter
			cmd.Stderr = &errWriter
			cmd.Stdin = nil
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("could not invoke 'docker': %v : %s", err, errWriter.String())
			}

			c.logger.Info("network created", "network", "nomad-"+net.Name)
		}
	}
	return nil
}

func dockerNetworkList(dockerBin string) ([]string, error) {
	var errWriter bytes.Buffer
	var outWriter bytes.Buffer

	cmd := exec.Command(
		dockerBin, "network", "ls",
		"--format", "{{.Name}}",
		"-q",
		"-f", "label=devconsul=1",
	)

	cmd.Stdout = &outWriter
	cmd.Stderr = &errWriter
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("could not invoke 'docker': %v : %s", err, errWriter.String())
	}

	var networks []string
	scan := bufio.NewScanner(&outWriter)
	for scan.Scan() {
		networks = append(networks, scan.Text())
	}
	if scan.Err() != nil {
		return nil, scan.Err()
	}

	sort.Strings(networks)

	return networks, nil
}

func dockerNetworkDelete(dockerBin string, name string) error {
	var errWriter bytes.Buffer
	var outWriter bytes.Buffer

	cmd := exec.Command(dockerBin, "network", "rm", name)
	cmd.Stdout = &outWriter
	cmd.Stderr = &errWriter
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not invoke 'docker': %v : %s", err, errWriter.String())
	}

	return nil
}

func dockerNetworkExists(dockerBin string, name string) (bool, error) {
	var errWriter bytes.Buffer
	var outWriter bytes.Buffer

	cmd := exec.Command(dockerBin, "network", "ls", "-q", "-f", "name="+name)
	cmd.Stdout = &outWriter
	cmd.Stderr = &errWriter
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("could not invoke 'docker': %v : %s", err, errWriter.String())
	}

	return outWriter.String() != "", nil
}

func dockerNetworkInspectCIDR(dockerBin string, name string) (string, error) {
	var errWriter bytes.Buffer
	var outWriter bytes.Buffer

	cmd := exec.Command(dockerBin, "network", "inspect", name)
	cmd.Stdout = &outWriter
	cmd.Stderr = &errWriter
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not invoke 'docker': %v : %s", err, errWriter.String())
	}

	type dockerInspect struct {
		IPAM struct {
			Config []map[string]string
		}
	}

	data := outWriter.Bytes()

	var got []dockerInspect
	if err := json.Unmarshal(data, &got); err != nil {
		return "", err
	}

	if len(got) != 1 {
		return "", fmt.Errorf("unexpected json inspect results: %s", string(data))
	}

	if len(got[0].IPAM.Config) != 1 {
		return "", fmt.Errorf("unexpected json inspect IPAM results: %s", string(data))
	}

	subnet := got[0].IPAM.Config[0]["Subnet"]

	if subnet == "" {
		return "", fmt.Errorf("unexpected json inspect subnet results: %s", string(data))
	}

	return subnet, nil
}
