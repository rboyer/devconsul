package main

import (
	"fmt"
	"io/ioutil"

	"github.com/hashicorp/hcl/v2/hclsimple"

	"github.com/hashicorp/consul/api"
)

type FlatConfig struct {
	ConsulImage          string
	EnvoyVersion         string
	CanaryConsulImage    string
	CanaryEnvoyVersion   string
	EncryptionTLS        bool
	EncryptionTLSAPI     bool
	EncryptionGossip     bool
	KubernetesEnabled    bool
	EnvoyLogLevel        string
	PrometheusEnabled    bool
	InitialMasterToken   string
	ConfigEntries        []api.ConfigEntry
	GossipKey            string
	AgentMasterToken     string
	EnterpriseEnabled    bool
	EnterpriseNamespaces []string
}

func (c *FlatConfig) Namespaces() []string {
	out := []string{"default"}
	out = append(out, c.EnterpriseNamespaces...)
	return out
}

type userConfig struct {
	ConsulImage        string                   `hcl:"consul_image,optional"`
	EnvoyVersion       string                   `hcl:"envoy_version,optional"`
	CanaryProxies      *userConfigCanaryProxies `hcl:"canary_proxies,block"`
	Encryption         *userConfigEncryption    `hcl:"encryption,block"`
	Kubernetes         *userConfigK8S           `hcl:"kubernetes,block"`
	Envoy              *userConfigEnvoy         `hcl:"envoy,block"`
	Monitor            *userConfigMonitor       `hcl:"monitor,block"`
	Topology           *userConfigTopology      `hcl:"topology,block"`
	Enterprise         *userConfigEnterprise    `hcl:"enterprise,block"`
	InitialMasterToken string                   `hcl:"initial_master_token,optional"`
	RawConfigEntries   []string                 `hcl:"config_entries,optional"`
}

func (uc *userConfig) DEPRECATED() {
	if len(uc.Topology.Datacenter) > 0 {
		uc.Topology.Datacenters = make(map[string]*userConfigTopologyDatacenter)
		for _, dc := range uc.Topology.Datacenter {
			uc.Topology.Datacenters[dc.Name] = dc
		}
	}
}

func (uc *userConfig) removeNilFields() {
	if uc.CanaryProxies == nil {
		uc.CanaryProxies = &userConfigCanaryProxies{}
	}
	if uc.Encryption == nil {
		uc.Encryption = &userConfigEncryption{}
	}
	if uc.Kubernetes == nil {
		uc.Kubernetes = &userConfigK8S{}
	}
	if uc.Envoy == nil {
		uc.Envoy = &userConfigEnvoy{}
	}
	if uc.Monitor == nil {
		uc.Monitor = &userConfigMonitor{}
	}
	if uc.Topology == nil {
		uc.Topology = &userConfigTopology{}
	}
	if uc.Enterprise == nil {
		uc.Enterprise = &userConfigEnterprise{}
	}
}

type userConfigMonitor struct {
	Prometheus bool `hcl:"prometheus,optional"`
}

type userConfigEnvoy struct {
	LogLevel string `hcl:"log_level,optional"`
}

type userConfigK8S struct {
	Enabled bool `hcl:"enabled,optional"`
}

type userConfigEncryption struct {
	TLS    bool `hcl:"tls,optional"`
	TLSAPI bool `hcl:"tls_api,optional"`
	Gossip bool `hcl:"gossip,optional"`
}

type userConfigCanaryProxies struct {
	ConsulImage  string `hcl:"consul_image,optional"`
	EnvoyVersion string `hcl:"envoy_version,optional"`
}

type userConfigEnterprise struct {
	Enabled    bool     `hcl:"enabled,optional"`
	Namespaces []string `hcl:"namespaces,optional"`
}

type userConfigTopology struct {
	NetworkShape        string                          `hcl:"network_shape,optional"`
	DisableWANBootstrap bool                            `hcl:"disable_wan_bootstrap,optional"`
	Datacenter          []*userConfigTopologyDatacenter `hcl:"datacenter,block"`
	Nodes               []*userConfigTopologyNodeConfig `hcl:"node,block"`

	Datacenters map[string]*userConfigTopologyDatacenter
}

func (t *userConfigTopology) NodeMap() map[string]*userConfigTopologyNodeConfig {
	m := make(map[string]*userConfigTopologyNodeConfig)
	for _, n := range t.Nodes {
		m[n.NodeName] = n
	}
	return m
}

type userConfigTopologyDatacenter struct {
	Name         string `hcl:"name,label"`
	Servers      int    `hcl:"servers,optional"`
	Clients      int    `hcl:"clients,optional"`
	MeshGateways int    `hcl:"mesh_gateways,optional"`
}

type userConfigTopologyNodeConfig struct {
	NodeName                    string            `hcl:"name,label"`
	UpstreamName                string            `hcl:"upstream_name,optional"`
	UpstreamNamespace           string            `hcl:"upstream_namespace,optional"`
	UpstreamDatacenter          string            `hcl:"upstream_datacenter,optional"`
	UpstreamExtraHCL            string            `hcl:"upstream_extra_hcl,optional"`
	ServiceMeta                 map[string]string `hcl:"service_meta,optional"` // key -> val
	ServiceNamespace            string            `hcl:"service_namespace,optional"`
	UseBuiltinProxy             bool              `hcl:"use_builtin_proxy,optional"`
	Dead                        bool              `hcl:"dead,optional"`
	Canary                      bool              `hcl:"canary,optional"`
	RetainInPrimaryGatewaysList bool              `hcl:"retain_in_primary_gateways_list,optional"`
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

	if cfg.EnterpriseEnabled && cfg.KubernetesEnabled {
		return nil, nil, fmt.Errorf("kubernetes and enterprise are not compatible in this tool")
	}

	if !cfg.EnterpriseEnabled && len(cfg.EnterpriseNamespaces) > 0 {
		return nil, nil, fmt.Errorf("enterprise.namespaces cannot be configured when enterprise.enabled=false")
	}

	if cfg.EncryptionTLSAPI && !cfg.EncryptionTLS {
		return nil, nil, fmt.Errorf("encryption.tls_api=true requires encryption.tls=true")
	}

	if cfg.CanaryConsulImage == "" && cfg.CanaryEnvoyVersion != "" {
		return nil, nil, fmt.Errorf("canary_proxies.consul_image must be set if canary_proxies.envoy_verison is set")
	}
	if cfg.CanaryConsulImage != "" && cfg.CanaryEnvoyVersion == "" {
		return nil, nil, fmt.Errorf("canary_proxies.envoy_image must be set if canary_proxies.consul_image is set")
	}

	canaryConfigured := cfg.CanaryConsulImage != "" && cfg.CanaryEnvoyVersion != ""

	topology, err := InferTopology(uct, cfg.EnterpriseEnabled, canaryConfigured)
	if err != nil {
		return nil, nil, err
	}

	if topology.NetworkShape != NetworkShapeIslands && topology.DisableWANBootstrap {
		return nil, nil, fmt.Errorf("disable_wan_bootstrap requires network_shape=islands")
	}

	if topology.NetworkShape == NetworkShapeIslands && !cfg.EncryptionTLS {
		return nil, nil, fmt.Errorf("network_shape=%q requires TLS to be enabled to function", topology.NetworkShape)
	}

	if cfg.PrometheusEnabled && topology.NetworkShape != NetworkShapeFlat {
		return nil, nil, fmt.Errorf("enabling prometheus currently requires network_shape=flat")
	}

	return cfg, topology, nil
}

func parseConfigPartial(contents []byte) (*FlatConfig, *userConfigTopology, error) {
	var uc userConfig
	err := serialDecodeHCL(&uc, []string{
		"defaults.hcl", defaultUserConfig,
		"config.hcl", string(contents),
	})
	if err != nil {
		return nil, nil, err
	}

	uc.removeNilFields()
	uc.DEPRECATED()

	cfg := &FlatConfig{
		ConsulImage:          uc.ConsulImage,
		EnvoyVersion:         uc.EnvoyVersion,
		CanaryConsulImage:    uc.CanaryProxies.ConsulImage,
		CanaryEnvoyVersion:   uc.CanaryProxies.EnvoyVersion,
		EncryptionTLS:        uc.Encryption.TLS,
		EncryptionTLSAPI:     uc.Encryption.TLSAPI,
		EncryptionGossip:     uc.Encryption.Gossip,
		KubernetesEnabled:    uc.Kubernetes.Enabled,
		EnvoyLogLevel:        uc.Envoy.LogLevel,
		PrometheusEnabled:    uc.Monitor.Prometheus,
		InitialMasterToken:   uc.InitialMasterToken,
		EnterpriseEnabled:    uc.Enterprise.Enabled,
		EnterpriseNamespaces: uc.Enterprise.Namespaces,
	}

	for i, raw := range uc.RawConfigEntries {
		entry, err := api.DecodeConfigEntryFromJSON([]byte(raw))
		if err != nil {
			return nil, nil, fmt.Errorf("invalid config entry [%d]: %v", i, err)
		}
		cfg.ConfigEntries = append(cfg.ConfigEntries, entry)
	}

	return cfg, uc.Topology, nil
}

func serialDecodeHCL(out interface{}, configs []string) error {
	for i := 0; i < len(configs); i += 2 {
		name := configs[i]
		config := configs[i+1]
		if err := decodeHCL(out, name, config); err != nil {
			return err
		}
	}
	return nil
}

func decodeHCL(out interface{}, name, config string) (xerr error) {
	defer func() {
		if r := recover(); r != nil {
			panic(fmt.Sprintf(
				"could not parse and decode snippet %q: %v", name, r,
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

const defaultUserConfig = `
consul_image  = "consul-dev:latest"
envoy_version = "v1.16.0"
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
  datacenter "dc1" {
    servers       = 1
    clients       = 2
    mesh_gateways = 0
  }
}
`
