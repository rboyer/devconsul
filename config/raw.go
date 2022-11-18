package config

type rawConfigEnvelope struct {
	Active string       `hcl:"active,optional"`
	Config []*rawConfig `hcl:"config,block"`
}

func (e *rawConfigEnvelope) GetActive() (*rawConfig, bool) {
	for _, cfg := range e.Config {
		if cfg.Name == e.Active {
			return cfg, true
		}
	}
	return nil, false
}

// rawConfig is the top level structure representing the contents of a user
// provided config file. It is what the file is initially decoded into.
type rawConfig struct {
	Name          string                  `hcl:"name,label"`
	ConsulImage   string                  `hcl:"consul_image,optional"`
	EnvoyVersion  string                  `hcl:"envoy_version,optional"`
	CanaryProxies *rawConfigCanaryProxies `hcl:"canary_proxies,block"`
	Security      *rawConfigSecurity      `hcl:"security,block"`
	Kubernetes    *rawConfigK8S           `hcl:"kubernetes,block"`
	Envoy         *rawConfigEnvoy         `hcl:"envoy,block"`
	Monitor       *rawConfigMonitor       `hcl:"monitor,block"`
	Enterprise    *rawConfigEnterprise    `hcl:"enterprise,block"`
	Topology      *rawTopology            `hcl:"topology,block"`
	Clusters      []*rawClusterConfig     `hcl:"cluster_config,block"`

	DeprecatedRawConfigEntries []string `hcl:"config_entries,optional"`
}

type rawClusterConfig struct {
	Name             string   `hcl:"name,label"`
	RawConfigEntries []string `hcl:"config_entries,optional"`
}

func (uc *rawConfig) removeNilFields() {
	if uc.CanaryProxies == nil {
		uc.CanaryProxies = &rawConfigCanaryProxies{}
	}
	if uc.Security == nil {
		uc.Security = &rawConfigSecurity{}
	}
	if uc.Security.Encryption == nil {
		uc.Security.Encryption = &rawConfigEncryption{}
	}
	if uc.Security.Vault == nil {
		uc.Security.Vault = &rawConfigVault{}
	}
	if uc.Kubernetes == nil {
		uc.Kubernetes = &rawConfigK8S{}
	}
	if uc.Envoy == nil {
		uc.Envoy = &rawConfigEnvoy{}
	}
	if uc.Monitor == nil {
		uc.Monitor = &rawConfigMonitor{}
	}
	if uc.Topology == nil {
		uc.Topology = &rawTopology{}
	}
	if uc.Enterprise == nil {
		uc.Enterprise = &rawConfigEnterprise{}
	}
}

type rawConfigMonitor struct {
	Prometheus bool `hcl:"prometheus,optional"`
}

type rawConfigEnvoy struct {
	LogLevel string `hcl:"log_level,optional"`
}

type rawConfigK8S struct {
	Enabled bool `hcl:"enabled,optional"`
}

type rawConfigSecurity struct {
	DisableACLs              bool                 `hcl:"disable_acls,optional"`
	Encryption               *rawConfigEncryption `hcl:"encryption,block"`
	InitialMasterToken       string               `hcl:"initial_master_token,optional"`
	DisableDefaultIntentions bool                 `hcl:"disable_default_intentions,optional"`
	Vault                    *rawConfigVault      `hcl:"vault,block"`
}

type rawConfigVault struct {
	Enabled bool     `hcl:"enabled,optional"`
	Image   string   `hcl:"image,optional"`
	MeshCA  []string `hcl:"mesh_ca,optional"`
}

type rawConfigEncryption struct {
	TLS     bool `hcl:"tls,optional"`
	TLSAPI  bool `hcl:"tls_api,optional"`
	TLSGRPC bool `hcl:"tls_grpc,optional"`
	Gossip  bool `hcl:"gossip,optional"`
}

type rawConfigCanaryProxies struct {
	ConsulImage  string   `hcl:"consul_image,optional"`
	EnvoyVersion string   `hcl:"envoy_version,optional"`
	Nodes        []string `hcl:"nodes,optional"`
}

type rawConfigEnterprise struct {
	Enabled           bool         `hcl:"enabled,optional"`
	Partitions        []*Partition `hcl:"partition,block"`
	Segments          []string     `hcl:"segments,optional"`
	LicensePath       string       `hcl:"license_path,optional"`
	DisablePartitions bool         `hcl:"disable_partitions,optional"`
}

type rawTopology struct {
	NetworkShape string     `hcl:"network_shape,optional"`
	LinkMode     string     `hcl:"link_mode,optional"`
	Cluster      []*Cluster `hcl:"cluster,block"`
	Nodes        []*Node    `hcl:"node,block"`

	DeprecatedDatacenter []*Cluster `hcl:"datacenter,block"`
}

func (t *rawTopology) GetCluster(name string) (*Cluster, bool) {
	for _, c := range t.Cluster {
		if c.Name == name {
			return c, true
		}
	}
	return nil, false
}
