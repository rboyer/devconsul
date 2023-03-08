package app

import (
	"fmt"

	"github.com/rboyer/devconsul/app/tfgen"
	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

func (c *Core) runGenerate(primaryOnly bool) error {
	if err := checkHasInitRunOnce(); err != nil {
		return err
	}

	c.topology.WalkSilent(func(node *infra.Node) {
		c.logger.Info("Generating node",
			"kind", node.Kind,
			"name", node.Name,
			"dc", node.Cluster,
			"ip", node.LocalAddress(),
		)
	})

	if err := c.generateCatalogInfo(primaryOnly); err != nil {
		return err
	}

	if err := c.generateConfigs(primaryOnly); err != nil {
		return err
	}

	var extraFiles []*tfgen.FileResource
	if c.config.PrometheusEnabled {
		extraFiles = append(extraFiles,
			tfgen.GeneratePrometheusConfigFile(c.config, c.topology),
			tfgen.GrafanaPrometheus(),
			tfgen.GrafanaINI(),
		)
	}
	if c.config.VaultEnabled {
		extraFiles = append(extraFiles,
			tfgen.VaultConfig(),
		)
	}

	for _, fr := range extraFiles {
		if err := fr.Commit(c.logger); err != nil {
			return fmt.Errorf("error committing %q: %w", fr.Name(), err)
		}
	}

	return c.terraformApply()
}

func (c *Core) generateConfigs(primaryOnly bool) error {
	var networks []tfgen.Resource
	for _, net := range c.topology.Networks() {
		networks = append(networks, tfgen.DockerNetwork(net.DockerName(), net.CIDR))
	}

	// write it to a cache file just so we can detect full-destroy
	if res, err := tfgen.WriteHCLResourceFile(c.logger, networks, "cache/networks.tf", 0644); err != nil {
		return err
	} else if res == tfgen.UpdateResultModified {
		// You will need to do a full down/up cycle to switch network_shape.
		return fmt.Errorf("Networking changed significantly, so you'll have to destroy everything first with 'devconsul down'")
	}

	var (
		volumes    []tfgen.Resource
		images     []tfgen.Resource
		containers []tfgen.Resource
	)

	addVolume := func(name string) {
		volumes = append(volumes, tfgen.DockerVolume(name))
	}

	addImage := func(name, image string) {
		images = append(images, tfgen.DockerImage(name, image))
	}

	if c.config.PrometheusEnabled {
		addVolume("prometheus-data")
		addVolume("grafana-data")
	}

	addImage("pause", "registry.k8s.io/pause:3.3")
	addImage("consul", c.config.Versions.ConsulImage)
	addImage("consul-envoy", "local/consul-envoy:latest")
	addImage("consul-dataplane", "local/consul-dataplane:latest") //c.config.Versions.DataplaneImage)
	addImage("pingpong", "rboyer/pingpong:latest")
	addImage("clustertool", "local/clustertool:latest")

	if c.config.CanaryVersions.Envoy != "" {
		addImage("consul-envoy-canary", "local/consul-envoy-canary:latest")
	}

	if c.config.CanaryVersions.DataplaneImage != "" {
		addImage("consul-dataplane-canary", "local/consul-dataplane-canary:latest") //c.config.CanaryVersions.DataplaneImage)
	}

	if err := c.topology.Walk(func(node *infra.Node) error {
		if node.IsAgent() {
			addVolume(node.Name)
		}

		// NOTE: primaryOnly implies we still generate empty pods in the remote datacenters
		populatePodContents := true
		if primaryOnly {
			populatePodContents = node.Cluster == config.PrimaryCluster
		}
		myContainers, err := tfgen.GenerateNodeContainers(c.config, c.topology, c.cache, node, populatePodContents)
		if err != nil {
			return err
		}

		containers = append(containers, myContainers...)

		return nil
	}); err != nil {
		return err
	}

	if c.config.PrometheusEnabled {
		addImage("prometheus", "prom/prometheus:latest")
		addImage("grafana", "grafana/grafana-oss:9.3.2")

		containers = append(containers,
			tfgen.PrometheusContainer(),
			tfgen.GrafanaContainer(),
		)
	}

	if c.config.VaultEnabled {
		addImage("vault", c.config.VaultImage)
		addVolume("vault-data")

		containers = append(containers,
			tfgen.VaultContainer(),
		)
	}

	var res []tfgen.Resource
	res = append(res, networks...)
	res = append(res, volumes...)
	res = append(res, images...)
	res = append(res, containers...)

	_, err := tfgen.WriteHCLResourceFile(c.logger, res, "docker.tf", 0644)
	return err
}
