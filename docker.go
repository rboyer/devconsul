package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	"github.com/rboyer/safeio"
	"golang.org/x/crypto/blake2b"
)

func (c *Core) RunForceDocker() error {
	return c.buildDockerImages(true)
}

func (c *Core) runDocker() error {
	return c.buildDockerImages(false)
}

func (c *Core) buildDockerImages(force bool) error {
	// Check to see if we have any work to do.
	var currentHash string
	{
		hash, err := blake2b.New256(nil)
		if err != nil {
			return err
		}

		if err := addFileToHash(c.devconsulBin, hash); err != nil {
			return err
		}
		if err := addFileToHash("config.hcl", hash); err != nil {
			return err
		}
		if err := addFileToHash("Dockerfile-envoy", hash); err != nil {
			return err
		}

		hash.Write([]byte(c.config.EnvoyVersion))
		hash.Write([]byte(c.config.ConsulImage))

		currentHash = fmt.Sprintf("%x", hash.Sum(nil))
	}

	var priorHash string
	{
		b, err := ioutil.ReadFile("cache/docker.hash")
		if os.IsNotExist(err) {
			priorHash = ""
		} else if err != nil {
			return err
		} else {
			priorHash = string(b)
		}
	}

	if priorHash == currentHash && !force {
		c.logger.Info("skipping docker image generation")
		return nil
	}

	// tag base
	if err := c.dockerExec([]string{
		"tag",
		c.config.ConsulImage,
		"local/consul-base:latest",
	}, nil); err != nil {
		return err
	}

	// build
	if err := c.dockerExec([]string{
		"build",
		"--build-arg",
		"ENVOY_VERSION=" + c.config.EnvoyVersion,
		"-t", "local/consul-envoy",
		"-f", "Dockerfile-envoy",
		".",
	}, nil); err != nil {
		return err
	}

	// Checkpoint
	if _, err := safeio.WriteToFile(bytes.NewReader([]byte(currentHash)), "cache/docker.hash", 0644); err != nil {
		return err
	}

	return nil
}

func (c *Core) RunStopDC2() error {
	var (
		pods       = make(map[string][]string)
		containers = make(map[string][]string)
	)

	c.topology.WalkSilent(func(n *Node) {
		pods[n.Datacenter] = append(pods[n.Datacenter], n.Name+"-pod")
		containers[n.Datacenter] = append(containers[n.Datacenter], n.Name)
		if n.MeshGateway {
			containers[n.Datacenter] = append(containers[n.Datacenter], n.Name+"-mesh-gateway")
		}
	})

	args := []string{"stop"}
	args = append(args, containers["dc2"]...)

	if err := c.dockerExec(args, nil); err != nil {
		c.logger.Error("error stopping containers", "error", err)
	}

	// docker stop $$($(PROGRAM_NAME) config | jq -r '.containers.dc2[]')
	return nil
}

func (c *Core) dockerExec(args []string, stdout io.Writer) error {
	return cmdExec("docker", c.dockerBin, args, stdout)
}

func (c *Core) stopAllContainers() error {
	cids, err := c.listRunningContainers()
	if err != nil {
		return err
	}

	if len(cids) == 0 {
		return nil
	}

	namesForCID, err := c.namesForContainerIDs(cids)
	if err != nil {
		return err
	}

	for _, cid := range cids {
		name := namesForCID[cid]
		c.logger.Info("stopping container", "name", name)
	}

	args := []string{"stop"}
	args = append(args, cids...)

	if err := c.dockerExec(args, ioutil.Discard); err != nil {
		return err
	}

	return nil
}

func (c *Core) listRunningContainers() ([]string, error) {
	var rawCIDs bytes.Buffer
	if err := c.dockerExec([]string{"ps", "-q", "--filter", "label=devconsul=1"}, &rawCIDs); err != nil {
		return nil, err
	}

	var cids []string

	s := bufio.NewScanner(&rawCIDs)
	for s.Scan() {
		cid := strings.TrimSpace(s.Text())
		cids = append(cids, cid)
	}
	if s.Err() != nil {
		return nil, s.Err()
	}

	return cids, nil
}

func (c *Core) namesForContainerIDs(cids []string) (map[string]string, error) { // id->name
	ret := make(map[string]string)
	for _, cid := range cids {
		ret[cid] = cid // default to itself
	}

	if len(cids) == 0 {
		return ret, nil
	}

	args := []string{"inspect"}
	args = append(args, cids...)
	args = append(args, "-f", "{{.ID}},{{.Name}}")

	var out bytes.Buffer
	if err := c.dockerExec(args, &out); err != nil {
		return nil, err
	}

	s := bufio.NewScanner(&out)
	for s.Scan() {
		parts := strings.SplitN(s.Text(), ",", 2)
		fullID, name := parts[0], parts[1]

		name = strings.TrimLeft(name, "/")

		for short, _ := range ret {
			if strings.HasPrefix(fullID, short) {
				ret[short] = name
				break
			}
		}

	}
	if s.Err() != nil {
		return nil, s.Err()
	}

	// d inspect 6bdbdae69aab 3e22bd16fbef -f '{{.ID}},{{.Name}}'
	// 6bdbdae69aab7f036a8342f7891f9c0e43b9357093056fb698161200759302ee,/dc1-server2
	// 3e22bd16fbef85c1dd8c01d3638e60d745c3c84e331aea4d3e6096030106032c,/dc1-server3

	return ret, nil

}
