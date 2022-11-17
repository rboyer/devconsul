package config

import (
	"testing"

	"github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/require"
)

func TestParseConfig_EmptyInferDefaults(t *testing.T) {
	fc, err := parseConfig("fake.hcl", nil)
	require.NoError(t, err)

	require.Equal(t, &Config{
		ConfName:             "legacy",
		ConsulImage:          "consul-dev:latest",
		EnvoyLogLevel:        "info",
		EnvoyVersion:         "v1.23.1",
		TopologyNetworkShape: "flat",
		TopologyLinkMode:     "federate",
		TopologyClusters: []*Cluster{
			{Name: "dc1", Servers: 1, Clients: 2},
		},
		ConfigEntries: map[string][]api.ConfigEntry{},
		VaultAsMeshCA: make(map[string]struct{}),
	}, fc)
}

func TestParseConfig_BothFormats(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		body := `
		envoy_version = "v1.18.3"
		`
		fc, err := parseConfig("fake.hcl", []byte(body))
		require.NoError(t, err)
		require.Equal(t, &Config{
			ConfName:             "legacy",
			ConsulImage:          "consul-dev:latest",
			EnvoyLogLevel:        "info",
			EnvoyVersion:         "v1.18.3",
			TopologyNetworkShape: "flat",
			TopologyLinkMode:     "federate",
			TopologyClusters: []*Cluster{
				{Name: "dc1", Servers: 1, Clients: 2},
			},
			ConfigEntries: map[string][]api.ConfigEntry{},
			VaultAsMeshCA: make(map[string]struct{}),
		}, fc)
	})
	t.Run("new 1", func(t *testing.T) {
		body := `
		active = "beta"
		config "alpha" {
		  envoy_version = "v1.17.3"
		}
		config "beta" {
		  envoy_version = "v1.18.3"
		}
		`
		fc, err := parseConfig("fake.hcl", []byte(body))
		require.NoError(t, err)
		require.Equal(t, &Config{
			ConfName:             "beta",
			ConsulImage:          "consul-dev:latest",
			EnvoyLogLevel:        "info",
			EnvoyVersion:         "v1.18.3",
			TopologyNetworkShape: "flat",
			TopologyLinkMode:     "federate",
			TopologyClusters: []*Cluster{
				{Name: "dc1", Servers: 1, Clients: 2},
			},
			ConfigEntries: map[string][]api.ConfigEntry{},
			VaultAsMeshCA: make(map[string]struct{}),
		}, fc)
	})
	t.Run("new 2", func(t *testing.T) {
		body := `
		active = "alpha"
		config "alpha" {
		  envoy_version = "v1.17.3"
		}
		config "beta" {
		  envoy_version = "v1.18.3"
		}
		`
		fc, err := parseConfig("fake.hcl", []byte(body))
		require.NoError(t, err)
		require.Equal(t, &Config{
			ConfName:             "alpha",
			ConsulImage:          "consul-dev:latest",
			EnvoyLogLevel:        "info",
			EnvoyVersion:         "v1.17.3",
			TopologyNetworkShape: "flat",
			TopologyLinkMode:     "federate",
			TopologyClusters: []*Cluster{
				{Name: "dc1", Servers: 1, Clients: 2},
			},
			ConfigEntries: map[string][]api.ConfigEntry{},
			VaultAsMeshCA: make(map[string]struct{}),
		}, fc)
	})
}

func TestParseConfig_AllFields(t *testing.T) {
	t.Run("datacenter", func(t *testing.T) {
		testParseConfig_AllFields(t, false)
	})
	t.Run("peer", func(t *testing.T) {
		testParseConfig_AllFields(t, true)
	})
}
func testParseConfig_AllFields(t *testing.T, peerInsteadOfDatacenter bool) {
	upstreamField := `upstream_datacenter = "fake-dc"`
	if peerInsteadOfDatacenter {
		upstreamField = `upstream_peer = "fake-peer"`
	}
	body := `
		consul_image = "my-dev-image:blah"
		envoy_version = "v1.18.3"
		canary_proxies {
			consul_image = "consul:1.9.5"
			envoy_version = "v1.17.2"
			nodes = [ "abc", "def" ]
		}
		security {
			disable_acls = true
			encryption {
				tls = true
				tls_api = true
				gossip = true
			}
			initial_master_token = "root"
			vault {
				enabled = true
				image   = "my-fake-image:3333"
				mesh_ca = ["dc2"]
			}
		}
		kubernetes {
			enabled = true
		}
		envoy {
			log_level = "debug"
		}
		monitor {
			prometheus = true
		}
		enterprise {
			enabled = true
			license_path = "/tmp/foo.hclic"
			partition "alpha" {
			}
			partition "beta" {
				namespaces = ["foo", "bar"]
			}
			segments = ["seg1", "seg2"]
		}
		topology {
			network_shape = "islands"
			link_mode     = "peer"
			cluster "dc1" {
				servers = 3
				clients = 2
				mesh_gateways = 1
			}
			cluster "dc2" {
				servers = 3
				clients = 2
				mesh_gateways = 1
			}
			node "dc1-client2" {
				upstream_name = "fake-service"
				upstream_namespace = "foo"
				` + upstreamField + `
				upstream_partition = "fake-ap"
				upstream_extra_hcl = "super invalid"
				service_meta ={
					version = "v2"
				}
				service_namespace = "bar"
				use_builtin_proxy = true
				dead = true
				retain_in_primary_gateways_list = true
			}
		}
		config_entries = [
			<<EOF
{
    "Kind": "proxy-defaults",
    "Name": "global",
    "Config": {
        "protocol": "http"
    },
    "MeshGateway": {
        "Mode": "local"
    }
}
EOF
		  ,
			<<EOF
{
 "Kind": "service-resolver",
 "Name": "pong",
 "Redirect": {
	 "Datacenter": "dc2"
 }
}
EOF
		,
		]
`
	fc, err := parseConfig("fake.hcl", []byte(body))
	require.NoError(t, err)

	expected := &Config{
		ConfName:            "legacy",
		ConsulImage:         "my-dev-image:blah",
		EnvoyVersion:        "v1.18.3",
		CanaryConsulImage:   "consul:1.9.5",
		CanaryEnvoyVersion:  "v1.17.2",
		CanaryNodes:         []string{"abc", "def"},
		EncryptionTLS:       true,
		EncryptionTLSAPI:    true,
		EncryptionGossip:    true,
		SecurityDisableACLs: true,
		VaultEnabled:        true,
		VaultImage:          "my-fake-image:3333",
		VaultAsMeshCA: map[string]struct{}{
			"dc2": struct{}{},
		},
		KubernetesEnabled:     true,
		EnvoyLogLevel:         "debug",
		PrometheusEnabled:     true,
		InitialMasterToken:    "root",
		EnterpriseEnabled:     true,
		EnterpriseLicensePath: "/tmp/foo.hclic",
		EnterprisePartitions: []*Partition{
			{
				Name: "alpha",
			},
			{
				Name:       "beta",
				Namespaces: []string{"foo", "bar"},
			},
		},
		EnterpriseSegments:   map[string]int{"seg1": 8303, "seg2": 8304},
		TopologyNetworkShape: "islands",
		TopologyLinkMode:     "peer",
		TopologyClusters: []*Cluster{
			{Name: "dc1", Servers: 3, Clients: 2, MeshGateways: 1},
			{Name: "dc2", Servers: 3, Clients: 2, MeshGateways: 1},
		},
		TopologyNodes: []*Node{
			{
				NodeName:          "dc1-client2",
				UpstreamName:      "fake-service",
				UpstreamNamespace: "foo",
				UpstreamPartition: "fake-ap",
				UpstreamExtraHCL:  "super invalid",
				ServiceMeta: map[string]string{
					"version": "v2",
				},
				ServiceNamespace:            "bar",
				UseBuiltinProxy:             true,
				Dead:                        true,
				RetainInPrimaryGatewaysList: true,
			},
		},
		ConfigEntries: map[string][]api.ConfigEntry{
			"dc1": {
				&api.ProxyConfigEntry{
					Kind: api.ProxyDefaults,
					Name: api.ProxyConfigGlobal,
					Config: map[string]interface{}{
						"protocol": "http",
					},
					MeshGateway: api.MeshGatewayConfig{
						Mode: api.MeshGatewayModeLocal,
					},
				},
				&api.ServiceResolverConfigEntry{
					Kind: api.ServiceResolver,
					Name: "pong",
					Redirect: &api.ServiceResolverRedirect{
						Datacenter: "dc2",
					},
				},
			},
		},
	}
	if peerInsteadOfDatacenter {
		expected.TopologyNodes[0].UpstreamPeer = "fake-peer"
		expected.TopologyNodes[0].UpstreamDatacenter = ""
	} else {
		expected.TopologyNodes[0].UpstreamPeer = ""
		expected.TopologyNodes[0].UpstreamDatacenter = "fake-dc"
	}
	require.Equal(t, expected, fc)
}
