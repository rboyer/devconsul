package main

import (
	"fmt"
	"io/ioutil"

	"github.com/hashicorp/consul/api"
)

type FlatConfig struct {
	ConsulImage        string
	EncryptionTLS      bool
	EncryptionGossip   bool
	KubernetesEnabled  bool
	EnvoyLogLevel      string
	PrometheusEnabled  bool
	InitialMasterToken string
	ConfigEntries      []api.ConfigEntry
	GossipKey          string
	AgentMasterToken   string
}

type userConfig struct {
	ConsulImage string `hcl:"consul_image"`
	Encryption  struct {
		TLS    bool `hcl:"tls"`
		Gossip bool `hcl:"gossip"`
	} `hcl:"encryption"`
	Kubernetes struct {
		Enabled bool `hcl:"enabled"`
	} `hcl:"kubernetes"`
	Envoy struct {
		LogLevel string `hcl:"log_level"`
	} `hcl:"envoy"`
	Monitor struct {
		Prometheus bool `hcl:"prometheus"`
	} `hcl:"monitor"`
	Topology           userConfigTopology `hcl:"topology"`
	InitialMasterToken string             `hcl:"initial_master_token"`
	RawConfigEntries   []string           `hcl:"config_entries"`
}

type userConfigTopology struct {
	NetworkShape string                                  `hcl:"network_shape"`
	Datacenters  map[string]userConfigTopologyDatacenter `hcl:"datacenters"`
	NodeConfig   map[string]userConfigTopologyNodeConfig `hcl:"node_config"` // node -> data
}

type userConfigTopologyDatacenter struct {
	Servers      int `hcl:"servers"`
	Clients      int `hcl:"clients"`
	MeshGateways int `hcl:"mesh_gateways"`
}

type userConfigTopologyNodeConfig struct {
	UpstreamName       string            `hcl:"upstream_name"`
	UpstreamDatacenter string            `hcl:"upstream_datacenter"`
	UpstreamExtraHCL   string            `hcl:"upstream_extra_hcl"`
	ServiceMeta        map[string]string `hcl:"service_meta"` // key -> val
	UseBuiltinProxy    bool              `hcl:"use_builtin_proxy"`
}

func (c *userConfigTopologyNodeConfig) Meta() map[string]string {
	if c.ServiceMeta == nil {
		return map[string]string{}
	}
	return c.ServiceMeta
}

func LoadConfig() (*FlatConfig, *Topology, error) {
	contents, err := ioutil.ReadFile("config.hcl")
	if err != nil {
		return nil, nil, err
	}

	return parseConfig(contents)
}

func parseConfig(contents []byte) (*FlatConfig, *Topology, error) {
	cfg, uct, err := parseConfigPartial(contents)
	if err != nil {
		return nil, nil, err
	}

	topology, err := InferTopology(uct)
	if err != nil {
		return nil, nil, err
	}

	if topology.NetworkShape == NetworkShapeIslands && !cfg.EncryptionTLS {
		return nil, nil, fmt.Errorf("network_shape=%q requires TLS to be enabled to function", topology.NetworkShape)
	}

	return cfg, topology, nil
}

func parseConfigPartial(contents []byte) (*FlatConfig, *userConfigTopology, error) {
	var uc userConfig
	err := serialDecodeHCL(&uc, []string{
		defaultUserConfig,
		string(contents),
	})
	if err != nil {
		return nil, nil, err
	}

	cfg := &FlatConfig{
		ConsulImage:        uc.ConsulImage,
		EncryptionTLS:      uc.Encryption.TLS,
		EncryptionGossip:   uc.Encryption.Gossip,
		KubernetesEnabled:  uc.Kubernetes.Enabled,
		EnvoyLogLevel:      uc.Envoy.LogLevel,
		PrometheusEnabled:  uc.Monitor.Prometheus,
		InitialMasterToken: uc.InitialMasterToken,
	}

	for i, raw := range uc.RawConfigEntries {
		entry, err := api.DecodeConfigEntryFromJSON([]byte(raw))
		if err != nil {
			return nil, nil, fmt.Errorf("invalid config entry [%d]: %v", i, err)
		}
		cfg.ConfigEntries = append(cfg.ConfigEntries, entry)
	}

	return cfg, &uc.Topology, nil
}

const defaultUserConfig = `
consul_image = "consul-dev:latest"
envoy {
  log_level = "info"
}
kubernetes {
  enabled = false
}
monitor {
  prometheus = false
}
topology {
  network_shape = "flat"
  datacenters {
    dc1 {
      servers = 1
      clients = 2
      mesh_gateways = 0
    }
  }
}
`
