package main

import (
	"errors"

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
		},
	}
}

type Config struct {
	ConsulImage        string           `hcl:"consul_image"`
	Encryption         ConfigEncryption `hcl:"encryption"`
	Kubernetes         ConfigKubernetes `hcl:"kubernetes"`
	Envoy              ConfigEnvoy      `hcl:"envoy"`
	Topology           ConfigTopology   `hcl:"topology"`
	InitialMasterToken string           `hcl:"initial_master_token"`
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

type ConfigTopology struct {
	Servers ConfigTopologyDatacenter `hcl:"servers"`
	Clients ConfigTopologyDatacenter `hcl:"clients"`
}

type ConfigTopologyDatacenter struct {
	Datacenter1 int `hcl:"dc1"`
	Datacenter2 int `hcl:"dc2"`
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
	if cfg.Topology.Servers.Datacenter2 < 0 {
		return nil, errors.New("dc2: has an invalid number of servers")
	}

	if cfg.Topology.Clients.Datacenter1 < 0 {
		return nil, errors.New("dc1: has an invalid number of clients")
	}
	if cfg.Topology.Clients.Datacenter2 < 0 {
		return nil, errors.New("dc2: has an invalid number of clients")
	}

	return &cfg, nil
}
