package main

import (
	"testing"

	"github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/require"
)

func TestParseConfigPartial_EmptyInferDefaults(t *testing.T) {
	fc, uct, err := parseConfigPartial(nil)
	require.NoError(t, err)

	require.Equal(t, &FlatConfig{
		ConsulImage:   "consul-dev:latest",
		EnvoyLogLevel: "info",
		EnvoyVersion:  "v1.16.0",
	}, fc)

	var expectUCT userConfigTopology
	expectUCT.NetworkShape = "flat"
	expectUCT.Datacenters = map[string]userConfigTopologyDatacenter{
		"dc1": {Servers: 1, Clients: 2},
	}

	require.Equal(t, expectUCT, *uct)
}

func TestParseConfigPartial_AllFields(t *testing.T) {
	body := `
		consul_image = "my-dev-image:blah"
		encryption {
			tls = true
			gossip = true
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
		topology {
			network_shape = "islands"
			datacenters {
				dc1 {
					servers = 3
					clients = 2
					mesh_gateways = 1
				}
				dc2 {
					servers = 3
					clients = 2
					mesh_gateways = 1
				}
			}
			node_config {
				dc1-client2 {
					upstream_name = "fake-service"
					upstream_datacenter = "fake-dc"
					upstream_extra_hcl = "super invalid"
					service_meta {
						version = "v2"
					}
					use_builtin_proxy = true
				}
			}
		}
		initial_master_token = "root"
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
	fc, uct, err := parseConfigPartial([]byte(body))
	require.NoError(t, err)

	require.Equal(t, &FlatConfig{
		ConsulImage:        "my-dev-image:blah",
		EncryptionTLS:      true,
		EncryptionGossip:   true,
		KubernetesEnabled:  true,
		PrometheusEnabled:  true,
		EnvoyLogLevel:      "debug",
		EnvoyVersion:       "v1.16.0",
		InitialMasterToken: "root",
		ConfigEntries: []api.ConfigEntry{
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
	}, fc)

	var expectUCT userConfigTopology
	expectUCT.NetworkShape = "islands"
	expectUCT.Datacenters = map[string]userConfigTopologyDatacenter{
		"dc1": {Servers: 3, Clients: 2, MeshGateways: 1},
		"dc2": {Servers: 3, Clients: 2, MeshGateways: 1},
	}
	expectUCT.NodeConfig = map[string]userConfigTopologyNodeConfig{
		"dc1-client2": {
			UpstreamName:       "fake-service",
			UpstreamDatacenter: "fake-dc",
			UpstreamExtraHCL:   "super invalid",
			ServiceMeta: map[string]string{
				"version": "v2",
			},
			UseBuiltinProxy: true,
		},
	}

	require.Equal(t, expectUCT, *uct)
}
