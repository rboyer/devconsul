package config

import (
	"fmt"
	"io/ioutil"

	"github.com/hashicorp/hcl/v2/hclsimple"

	"github.com/hashicorp/consul/api"
)

// LoadConfig loads up the default config file (config.hcl), parses it, and
// does some light validation.
func LoadConfig(pathname string) (*Config, error) {
	contents, err := ioutil.ReadFile(pathname)
	if err != nil {
		return nil, err
	}

	cfg, err := parseConfig(pathname, contents)
	if err != nil {
		return nil, err
	}

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func parseConfig(pathname string, contents []byte) (*Config, error) {
	// Extract the actively selected configuration.
	uc, err := decodeConfig(pathname, contents)
	if err != nil {
		return nil, err
	}

	uc.removeNilFields()

	if uc.ConsulImage == "" {
		uc.ConsulImage = "consul-dev:latest"
	}
	if uc.EnvoyVersion == "" {
		uc.EnvoyVersion = "v1.16.0"
	}
	if uc.Envoy.LogLevel == "" {
		uc.Envoy.LogLevel = "info"
	}
	if uc.Topology.NetworkShape == "" {
		uc.Topology.NetworkShape = "flat"
	}

	if _, ok := uc.Topology.GetDatacenter(PrimaryDC); !ok {
		uc.Topology.Datacenter = append(uc.Topology.Datacenter, &Datacenter{
			Name:    PrimaryDC,
			Servers: 1,
			Clients: 2,
		})
	}

	cfg := &Config{
		ConfName:              uc.Name,
		ConsulImage:           uc.ConsulImage,
		EnvoyVersion:          uc.EnvoyVersion,
		CanaryConsulImage:     uc.CanaryProxies.ConsulImage,
		CanaryEnvoyVersion:    uc.CanaryProxies.EnvoyVersion,
		CanaryNodes:           uc.CanaryProxies.Nodes,
		EncryptionTLS:         uc.Security.Encryption.TLS,
		EncryptionTLSAPI:      uc.Security.Encryption.TLSAPI,
		EncryptionGossip:      uc.Security.Encryption.Gossip,
		KubernetesEnabled:     uc.Kubernetes.Enabled,
		EnvoyLogLevel:         uc.Envoy.LogLevel,
		PrometheusEnabled:     uc.Monitor.Prometheus,
		InitialMasterToken:    uc.Security.InitialMasterToken,
		EnterpriseEnabled:     uc.Enterprise.Enabled,
		EnterprisePartitions:  uc.Enterprise.Partitions,
		EnterpriseLicensePath: uc.Enterprise.LicensePath,
		TopologyNetworkShape:  uc.Topology.NetworkShape,
		DisableWANBootstrap:   uc.Topology.DisableWANBootstrap,
		TopologyDatacenters:   uc.Topology.Datacenter,
		TopologyNodes:         uc.Topology.Nodes,
	}

	for i, raw := range uc.RawConfigEntries {
		entry, err := api.DecodeConfigEntryFromJSON([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid config entry [%d]: %v", i, err)
		}
		cfg.ConfigEntries = append(cfg.ConfigEntries, entry)
	}

	return cfg, nil
}

// Extract the actively selected configuration.
func decodeConfig(pathname string, contents []byte) (*rawConfig, error) {
	// check legacy first
	{
		var raw rawConfig
		err := decodeHCL(&raw, pathname, string(contents))
		if err == nil {
			raw.Name = "legacy"
			return &raw, nil
		}
	}

	// assume non legacy
	var envelope rawConfigEnvelope
	err := decodeHCL(&envelope, pathname, string(contents))
	if err != nil {
		return nil, err
	}
	if envelope.Active == "" {
		return nil, fmt.Errorf("missing required field 'active'")
	}

	got, ok := envelope.GetActive()
	if !ok {
		return nil, fmt.Errorf("active configuration %q is not defined", envelope.Active)
	}
	return got, nil
}

func decodeHCL(out interface{}, name, config string) (xerr error) {
	defer func() {
		if r := recover(); r != nil {
			panic(fmt.Sprintf(
				"panic: could not parse and decode snippet %q: %v", name, r,
			))
		}
	}()
	err := hclsimple.Decode(
		name,
		[]byte(config),
		nil,
		out,
	)
	if err != nil {
		return fmt.Errorf("could not parse and decode snippet %q: %v", name, err)
	}
	return nil
}

func validateConfig(cfg *Config) error {
	if cfg.EnterpriseEnabled && cfg.KubernetesEnabled {
		return fmt.Errorf("kubernetes and enterprise are not compatible in this tool")
	}

	if !cfg.EnterpriseEnabled && cfg.EnterpriseLicensePath != "" {
		cfg.EnterpriseLicensePath = "" // zero it out
	}

	if cfg.EnterpriseEnabled && cfg.EnterpriseLicensePath == "" {
		return fmt.Errorf("enterprise.license_path is required when enterprise.enabled=true")
	}

	if !cfg.EnterpriseEnabled && len(cfg.EnterprisePartitions) > 0 {
		return fmt.Errorf("enterprise.partitions cannot be configured when enterprise.enabled=false")
	}

	if !cfg.EnterpriseEnabled {
		for _, node := range cfg.TopologyNodes {
			if node.Partition != "" {
				return fmt.Errorf("nodes cannot be assigned partitions when enterprise.enabled=false")
			}
			if node.UpstreamPartition != "" {
				return fmt.Errorf("upstreams cannot be assigned partitions when enterprise.enabled=false")
			}
			if node.ServiceNamespace != "" {
				return fmt.Errorf("namespaces cannot be configured on services when enterprise.enabled=false")
			}
			if node.UpstreamNamespace != "" {
				return fmt.Errorf("upstreams cannot be assigned namespaces when enterprise.enabled=false")
			}
		}
	}

	hasSecondaryDatacenter := false
	for _, dc := range cfg.TopologyDatacenters {
		if dc.Name != PrimaryDC {
			hasSecondaryDatacenter = true
		}
	}

	if len(cfg.EnterprisePartitions) > 0 {
		if hasSecondaryDatacenter {
			return fmt.Errorf("enterprise.partitions and topology.datacenter are mutually exclusive")
		}
		seen := make(map[string]struct{})
		for _, ap := range cfg.EnterprisePartitions {
			if ap.Name == "" {
				return fmt.Errorf("enterprise.partitions must refer to the default partition as %q", "default")
			}
			if _, ok := seen[ap.Name]; ok {
				return fmt.Errorf("enterprise.partitions contains a duplicate for %q", ap.Name)
			}
			seen[ap.Name] = struct{}{}

			seenNS := make(map[string]struct{})
			for _, ns := range ap.Namespaces {
				if ns == "" {
					return fmt.Errorf("enterprise.partitions[%q].namespaces must refer to the default namespace as %q", ap.Name, "default")
				}
				if _, ok := seenNS[ns]; ok {
					return fmt.Errorf("enterprise.partitions[%q].namespaces contains a duplicate for %q", ap.Name, ns)
				}
				seenNS[ns] = struct{}{}
			}
		}
	}

	if cfg.EncryptionTLSAPI && !cfg.EncryptionTLS {
		return fmt.Errorf("encryption.tls_api=true requires encryption.tls=true")
	}

	if cfg.CanaryConsulImage == "" && cfg.CanaryEnvoyVersion != "" {
		return fmt.Errorf("canary_proxies.consul_image must be set if canary_proxies.envoy_verison is set")
	}
	if cfg.CanaryConsulImage != "" && cfg.CanaryEnvoyVersion == "" {
		return fmt.Errorf("canary_proxies.envoy_image must be set if canary_proxies.consul_image is set")
	}

	return nil
}
