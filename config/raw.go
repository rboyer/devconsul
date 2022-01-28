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
	Name             string                  `hcl:"name,label"`
	ConsulImage      string                  `hcl:"consul_image,optional"`
	EnvoyVersion     string                  `hcl:"envoy_version,optional"`
	CanaryProxies    *rawConfigCanaryProxies `hcl:"canary_proxies,block"`
	Security         *rawConfigSecurity      `hcl:"security,block"`
	Kubernetes       *rawConfigK8S           `hcl:"kubernetes,block"`
	Envoy            *rawConfigEnvoy         `hcl:"envoy,block"`
	Monitor          *rawConfigMonitor       `hcl:"monitor,block"`
	Enterprise       *rawConfigEnterprise    `hcl:"enterprise,block"`
	Topology         *rawTopology            `hcl:"topology,block"`
	RawConfigEntries []string                `hcl:"config_entries,optional"`
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
	Encryption         *rawConfigEncryption `hcl:"encryption,block"`
	InitialMasterToken string               `hcl:"initial_master_token,optional"`
}

type rawConfigEncryption struct {
	TLS    bool `hcl:"tls,optional"`
	TLSAPI bool `hcl:"tls_api,optional"`
	Gossip bool `hcl:"gossip,optional"`
}

type rawConfigCanaryProxies struct {
	ConsulImage  string   `hcl:"consul_image,optional"`
	EnvoyVersion string   `hcl:"envoy_version,optional"`
	Nodes        []string `hcl:"nodes,optional"`
}

type rawConfigEnterprise struct {
	Enabled           bool         `hcl:"enabled,optional"`
	Partitions        []*Partition `hcl:"partition,block"`
	LicensePath       string       `hcl:"license_path,optional"`
	DisablePartitions bool         `hcl:"disable_partitions,optional"`
}

type rawTopology struct {
	NetworkShape        string        `hcl:"network_shape,optional"`
	DisableWANBootstrap bool          `hcl:"disable_wan_bootstrap,optional"`
	Datacenter          []*Datacenter `hcl:"datacenter,block"`
	Nodes               []*Node       `hcl:"node,block"`
}

func (t *rawTopology) GetDatacenter(name string) (*Datacenter, bool) {
	for _, dc := range t.Datacenter {
		if dc.Name == name {
			return dc, true
		}
	}
	return nil, false
}
