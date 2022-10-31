package app

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rboyer/safeio"
	"golang.org/x/crypto/blake2b"

	"github.com/rboyer/devconsul/infra"
)

func (a *App) RunForceDocker() error {
	return a.buildDockerImages(true)
}

func (a *App) buildDockerImages(force bool) error {
	// Check to see if we have any work to do.
	var currentHash string
	{
		hash, err := blake2b.New256(nil)
		if err != nil {
			return err
		}

		if err := addFileToHash(a.runner.GetPathToSelf(), hash); err != nil {
			return err
		}
		if err := addFileToHash(DefaultConfigFile, hash); err != nil {
			return err
		}
		if err := addFileToHash("Dockerfile-envoy", hash); err != nil {
			return err
		}

		hash.Write([]byte(a.config.EnvoyVersion))
		hash.Write([]byte(a.config.ConsulImage))
		hash.Write([]byte(a.config.CanaryEnvoyVersion))
		hash.Write([]byte(a.config.CanaryConsulImage))

		currentHash = fmt.Sprintf("%x", hash.Sum(nil))
	}

	var priorHash string
	{
		b, err := os.ReadFile("cache/docker.hash")
		if os.IsNotExist(err) {
			priorHash = ""
		} else if err != nil {
			return err
		} else {
			priorHash = string(b)
		}
	}

	if priorHash == currentHash && !force {
		a.logger.Info("skipping docker image generation")
		return nil
	}

	// tag base
	if err := a.runner.DockerExec([]string{
		"tag",
		a.config.ConsulImage,
		"local/consul-base:latest",
	}, nil); err != nil {
		return err
	}

	if a.config.CanaryConsulImage != "" {
		if err := a.runner.DockerExec([]string{
			"tag",
			a.config.CanaryConsulImage,
			"local/consul-base-canary:latest",
		}, nil); err != nil {
			return err
		}
	}

	// build
	if err := a.runner.DockerExec([]string{
		"build",
		"--build-arg",
		"CONSUL_IMAGE=local/consul-base:latest",
		"--build-arg",
		"ENVOY_VERSION=" + a.config.EnvoyVersion,
		"-t", "local/consul-envoy",
		"-f", "Dockerfile-envoy",
		".",
	}, nil); err != nil {
		return err
	}

	if a.config.CanaryEnvoyVersion != "" {
		if err := a.runner.DockerExec([]string{
			"build",
			"--build-arg",
			"CONSUL_IMAGE=local/consul-base-canary:latest",
			"--build-arg",
			"ENVOY_VERSION=" + a.config.CanaryEnvoyVersion,
			"-t", "local/consul-envoy-canary",
			"-f", "Dockerfile-envoy",
			".",
		}, nil); err != nil {
			return err
		}
	}

	// Checkpoint
	if _, err := safeio.WriteToFile(bytes.NewReader([]byte(currentHash)), "cache/docker.hash", 0644); err != nil {
		return err
	}

	return nil
}

func (a *App) RunStopDC2() error {
	return a.runStopDC2()
}

func (a *App) runStopDC2() error {
	var (
		pods       = make(map[string][]string)
		containers = make(map[string][]string)
	)

	a.topology.WalkSilent(func(n *infra.Node) {
		pods[n.Cluster] = append(pods[n.Cluster], n.Name+"-pod")
		containers[n.Cluster] = append(containers[n.Cluster], n.Name)
		if n.MeshGateway {
			containers[n.Cluster] = append(containers[n.Cluster], n.Name+"-mesh-gateway")
		}
	})

	args := []string{"stop"}
	args = append(args, containers["dc2"]...)

	if err := a.runner.DockerExec(args, nil); err != nil {
		a.logger.Error("error stopping containers", "error", err)
	}

	// docker stop $$($(PROGRAM_NAME) config | jq -r '.containers.dc2[]')
	return nil
}

func (a *App) stopAllContainers() error {
	cids, err := a.listRunningContainers()
	if err != nil {
		return err
	}

	if len(cids) == 0 {
		return nil
	}

	namesForCID, err := a.namesForContainerIDs(cids)
	if err != nil {
		return err
	}

	for _, cid := range cids {
		name := namesForCID[cid]
		a.logger.Info("stopping container", "name", name)
	}

	args := []string{"stop"}
	args = append(args, cids...)

	if err := a.runner.DockerExec(args, io.Discard); err != nil {
		return err
	}

	return nil
}

func (a *App) listRunningContainers() ([]string, error) {
	var rawCIDs bytes.Buffer
	if err := a.runner.DockerExec([]string{"ps", "-q", "--filter", "label=devconsul=1"}, &rawCIDs); err != nil {
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

func (a *App) namesForContainerIDs(cids []string) (map[string]string, error) { // id->name
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
	if err := a.runner.DockerExec(args, &out); err != nil {
		return nil, err
	}

	s := bufio.NewScanner(&out)
	for s.Scan() {
		parts := strings.SplitN(s.Text(), ",", 2)
		fullID, name := parts[0], parts[1]

		name = strings.TrimLeft(name, "/")

		for short := range ret {
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
