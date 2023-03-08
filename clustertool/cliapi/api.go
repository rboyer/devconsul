package cliapi

// Deprecated: delete
type HealthCheckConfig struct {
	Checks []*HealthCheck `json:",omitempty"`
}

// Deprecated: delete
type HealthCheck struct {
	Node      string `json:",omitempty"`
	Service   string `json:",omitempty"`
	Namespace string `json:",omitempty"`
	Partition string `json:",omitempty"`

	HTTP string `json:",omitempty"`
	TCP  string `json:",omitempty"`

	LastResultOK *bool `json:"-"`
}
