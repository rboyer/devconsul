package main

import (
	"errors"
	"fmt"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/hcl"
)

func DefaultConfig() Config {
	return Config{
		ConsulImage: "consul:1.5.0",
		Envoy: ConfigEnvoy{
			LogLevel: "info",
		},
		Topology: ConfigTopology{
			Servers: ConfigTopologyDatacenter{
				Datacenter1: 1,
				Datacenter2: 1,
			},
			Clients: ConfigTopologyDatacenter{
				Datacenter1: 2,
				Datacenter2: 2,
			},
			NodeConfig: map[string]ConfigTopologyNodeConfig{},
		},
	}
}

type Config struct {
	ConsulImage        string            `hcl:"consul_image"`
	Encryption         ConfigEncryption  `hcl:"encryption"`
	Kubernetes         ConfigKubernetes  `hcl:"kubernetes"`
	Envoy              ConfigEnvoy       `hcl:"envoy"`
	Monitor            ConfigMonitor     `hcl:"monitor"`
	Topology           ConfigTopology    `hcl:"topology"`
	InitialMasterToken string            `hcl:"initial_master_token"`
	RawConfigEntries   []string          `hcl:"config_entries"`
	ConfigEntries      []api.ConfigEntry `hcl:"-"`
}
type ConfigEncryption struct {
	TLS    bool `hcl:"tls"`
	Gossip bool `hcl:"gossip"`
}
type ConfigKubernetes struct {
	Enabled bool `hcl:"enabled"`
}
type ConfigEnvoy struct {
	LogLevel string `hcl:"log_level"`
}
type ConfigMonitor struct {
	Prometheus bool `hcl:"prometheus"`
}

type ConfigTopology struct {
	NetworkShape string                              `hcl:"network_shape"`
	Servers      ConfigTopologyDatacenter            `hcl:"servers"`
	Clients      ConfigTopologyDatacenter            `hcl:"clients"`
	NodeConfig   map[string]ConfigTopologyNodeConfig `hcl:"node_config"` // node -> data
}

type ConfigTopologyDatacenter struct {
	Datacenter1 int `hcl:"dc1"`
	Datacenter2 int `hcl:"dc2"`
	Datacenter3 int `hcl:"dc3"`
}

type ConfigTopologyNodeConfig struct {
	UpstreamName       string            `hcl:"upstream_name"`
	UpstreamDatacenter string            `hcl:"upstream_datacenter"`
	UpstreamExtraHCL   string            `hcl:"upstream_extra_hcl"`
	ServiceMeta        map[string]string `hcl:"service_meta"` // key -> val
	MeshGateway        bool              `hcl:"mesh_gateway"`
	UseBuiltinProxy    bool              `hcl:"use_builtin_proxy"`
}

func (c *ConfigTopologyNodeConfig) Meta() map[string]string {
	if c.ServiceMeta == nil {
		return map[string]string{}
	}
	return c.ServiceMeta
}

func LoadConfig() (*Config, error) {
	n, err := parseHCLFile("config.hcl")
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()
	if err := hcl.DecodeObject(&cfg, n); err != nil {
		return nil, err
	}

	if cfg.Topology.Servers.Datacenter1 <= 0 {
		return nil, errors.New("dc1: must always have at least one server")
	}
	if cfg.Topology.Servers.Datacenter2 <= 0 {
		return nil, errors.New("dc2: must always have at least one server")
	}
	if cfg.Topology.Servers.Datacenter3 <= 0 {
		return nil, errors.New("dc3: must always have at least one server")
	}

	if cfg.Topology.Clients.Datacenter1 <= 0 {
		return nil, errors.New("dc1: must always have at least one client")
	}
	if cfg.Topology.Clients.Datacenter2 <= 0 {
		return nil, errors.New("dc2: must always have at least one client")
	}
	if cfg.Topology.Clients.Datacenter3 <= 0 {
		return nil, errors.New("dc3: must always have at least one client")
	}

	for i, raw := range cfg.RawConfigEntries {
		entry, err := api.DecodeConfigEntryFromJSON([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid config entry [%d]: %v", i, err)
		}
		cfg.ConfigEntries = append(cfg.ConfigEntries, entry)
	}

	return &cfg, nil
}
