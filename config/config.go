package config

import (
	"github.com/hashicorp/consul/api"
)

// Config is the runtime configuration struct derived from rawConfig.
type Config struct {
	ConfName                    string // name from config.hcl
	ConsulImage                 string
	EnvoyVersion                string
	CanaryConsulImage           string
	CanaryEnvoyVersion          string
	CanaryNodes                 []string
	EncryptionTLS               bool
	EncryptionTLSAPI            bool
	EncryptionGossip            bool
	SecurityDisableACLs         bool
	KubernetesEnabled           bool
	EnvoyLogLevel               string
	PrometheusEnabled           bool
	InitialMasterToken          string
	ConfigEntries               map[string][]api.ConfigEntry
	GossipKey                   string
	AgentMasterToken            string
	EnterpriseEnabled           bool
	EnterprisePartitions        []*Partition
	EnterpriseDisablePartitions bool
	EnterpriseLicensePath       string
	TopologyNetworkShape        string
	TopologyLinkMode            string
	TopologyClusters            []*Cluster
	TopologyNodes               []*Node
}

func (c *Config) CanaryInfo() (configured bool, nodes map[string]struct{}) {
	configured = c.CanaryConsulImage != "" && c.CanaryEnvoyVersion != ""

	nodes = make(map[string]struct{})
	for _, n := range c.CanaryNodes {
		nodes[n] = struct{}{}
	}

	return configured, nodes
}

type Partition struct {
	Name       string   `hcl:"name,label"`
	Namespaces []string `hcl:"namespaces,optional"`
}

func (c *Partition) String() string {
	if c == nil || c.Name == "" {
		return "default"
	}
	return c.Name
}

func (c *Partition) IsDefault() bool {
	return c == nil || c.Name == "" || c.Name == "default"
}

type Cluster struct {
	Name         string `hcl:"name,label"`
	Servers      int    `hcl:"servers,optional"`
	Clients      int    `hcl:"clients,optional"`
	MeshGateways int    `hcl:"mesh_gateways,optional"`
}

type Node struct {
	NodeName           string            `hcl:"name,label"`
	Partition          string            `hcl:"partition,optional"`
	UpstreamName       string            `hcl:"upstream_name,optional"`
	UpstreamNamespace  string            `hcl:"upstream_namespace,optional"`
	UpstreamPartition  string            `hcl:"upstream_partition,optional"`
	UpstreamPeer       string            `hcl:"upstream_peer,optional"`
	UpstreamDatacenter string            `hcl:"upstream_datacenter,optional"`
	UpstreamExtraHCL   string            `hcl:"upstream_extra_hcl,optional"`
	ServiceMeta        map[string]string `hcl:"service_meta,optional"` // key -> val
	ServiceNamespace   string            `hcl:"service_namespace,optional"`
	UseBuiltinProxy    bool              `hcl:"use_builtin_proxy,optional"`
	Dead               bool              `hcl:"dead,optional"`

	// mesh-gateway settings
	RetainInPrimaryGatewaysList bool `hcl:"retain_in_primary_gateways_list,optional"`
	UseDNSWANAddress            bool `hcl:"use_dns_wan_address,optional"`
}

func (c *Node) Meta() map[string]string {
	if c.ServiceMeta == nil {
		return map[string]string{}
	}
	return c.ServiceMeta
}
