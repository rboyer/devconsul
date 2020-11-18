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
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/rboyer/safeio"
)

type CommandNomad struct {
	*Core

	g *CommandGenerate

	verbose bool
	destroy bool
}

func (c *CommandNomad) Run() error {

	flag.BoolVar(&c.verbose, "v", false, "verbose")
	flag.BoolVar(&c.destroy, "rm", false, "destroy")
	flag.Parse()

	if err := c.syncDockerNetworksTF(); err != nil {
		return err
	}
	// if err := c.syncDockerNetworks(); err != nil {
	// 	return err
	// }

	if err := c.generateJobFiles(); err != nil {
		return err
	}

	// TODO: prom + grafana stuff from gen.go

	return nil
}

func (c *CommandNomad) generateJobFiles() error {
	if err := os.MkdirAll("jobs", 0755); err != nil {
		return err
	}

	if c.verbose {
		c.topology.WalkSilent(func(node *Node) {
			c.logger.Info("Generating node",
				"name", node.Name,
				"server", node.Server,
				"dc", node.Datacenter,
				"ip", node.LocalAddress(),
			)
		})
	}

	err := c.topology.Walk(func(node *Node) error {
		podName := node.Name + "-pod"

		podHCL, err := c.generateAgentHCL(node)
		if err != nil {
			return err
		}

		jobHCL := fmt.Sprintf(`
job %[1]q {
  datacenters = ["dc1"]

  group "group" {
    task "pause" {
      driver = "docker"
      config {
        image = "k8s.gcr.io/pause:3.3"
		dns_servers = "8.8.8.8"
		hostname = %[1]q
		ipv4_address = %[2]q
		network_mode = 
      }
    }
  }
}
`, podName)

	})
	if err != nil {
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

	// NOTE: you will need to do a full down/up cycle to switch network_shape

	var buf bytes.Buffer
	for _, net := range c.topology.Networks() {
		buf.WriteString(strings.TrimSpace(fmt.Sprintf(`
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

`, net.NomadName(), net.NomadName(), net.CIDR,
		)) + "\n")
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
			name := net.NomadName()
			exists, err := dockerNetworkExists(dockerBin, name)
			if err != nil {
				return err
			}
			if exists {
				subnet, err := dockerNetworkInspectCIDR(dockerBin, name)
				if err != nil {
					return err
				}
				if subnet == net.CIDR {
					skipDeleteNetworks[name] = struct{}{}
				} else {
					createNetworks[name] = struct{}{}
				}
			} else {
				createNetworks[name] = struct{}{}
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
			name := net.NomadName()
			if _, ok := createNetworks[name]; !ok {
				if c.verbose {
					c.logger.Debug("network already exists", "network", name)
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
				name,
			)
			cmd.Stdout = &outWriter
			cmd.Stderr = &errWriter
			cmd.Stdin = nil
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("could not invoke 'docker': %v : %s", err, errWriter.String())
			}

			c.logger.Info("network created", "network", name)
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
