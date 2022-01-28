package infra

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/util"
)

func TestCompileTopology(t *testing.T) {
	type testcase struct {
		cfg            *config.Config
		canaryNodes    []string
		expectErr      bool
		expectExactErr string
		expectFn       func(t *testing.T, topo *Topology)
	}

	cases := map[string]testcase{
		"missing-primary": {
			cfg: &config.Config{
				TopologyNetworkShape: "flat",
				TopologyDatacenters: []*config.Datacenter{
					{
						Name:    "dc2",
						Servers: 1,
						Clients: 1,
					},
				},
			},
			expectExactErr: `primary datacenter "dc1" is missing from config`,
		},
		"full-islands": {
			cfg: &config.Config{
				CanaryConsulImage:    "consul-canary:latest",
				CanaryEnvoyVersion:   "v1.16.0",
				CanaryNodes:          []string{"dc2-client2"},
				EncryptionTLS:        true,
				TopologyNetworkShape: "islands",
				TopologyDatacenters: []*config.Datacenter{
					{
						Name:         "dc1",
						Servers:      3,
						Clients:      2,
						MeshGateways: 1,
					},
					{
						Name:         "dc2",
						Servers:      3,
						Clients:      2,
						MeshGateways: 1,
					},
				},
				TopologyNodes: []*config.Node{
					{
						NodeName: "dc1-client1",
						ServiceMeta: map[string]string{
							"foo": "bar",
							"RAB": "OOF",
						},
					},
					{
						NodeName:           "dc2-client2",
						UpstreamName:       "blah",
						UpstreamDatacenter: "fake",
						UpstreamPartition:  "also-fake",
						UpstreamExtraHCL:   "// not real",
						ServiceMeta: map[string]string{
							"AAA": "BBB",
						},
						UseBuiltinProxy: true,
					},
				},
			},
			expectFn: func(t *testing.T, topo *Topology) {
				expect := &Topology{
					NetworkShape: NetworkShapeIslands,
					networks: map[string]*Network{
						"dc1": {
							Name: "dc1",
							CIDR: "10.0.1.0/24",
						},
						"dc2": {
							Name: "dc2",
							CIDR: "10.0.2.0/24",
						},
						"wan": {
							Name: "wan",
							CIDR: "10.1.0.0/16",
						},
					},
					dcs: []*Datacenter{
						{
							Name:         "dc1",
							Primary:      true,
							Index:        1,
							Servers:      3,
							Clients:      3,
							MeshGateways: 1,
							BaseIP:       "10.0.1",
							WANBaseIP:    "10.1.1",
						},
						{
							Name:         "dc2",
							Primary:      false,
							Index:        2,
							Servers:      3,
							Clients:      3,
							MeshGateways: 1,
							BaseIP:       "10.0.2",
							WANBaseIP:    "10.1.2",
						},
					},
					nm: map[string]*Node{
						// ============= dc1 ==============
						"dc1-server1": {
							Index:      0,
							Datacenter: "dc1",
							Name:       "dc1-server1",
							Partition:  "default",
							Server:     true,
							Addresses: []Address{
								{
									Network:   "dc1",
									IPAddress: "10.0.1.11",
								},
							},
						},
						"dc1-server2": {
							Index:      1,
							Datacenter: "dc1",
							Name:       "dc1-server2",
							Partition:  "default",
							Server:     true,
							Addresses: []Address{
								{
									Network:   "dc1",
									IPAddress: "10.0.1.12",
								},
							},
						},
						"dc1-server3": {
							Index:      2,
							Datacenter: "dc1",
							Name:       "dc1-server3",
							Partition:  "default",
							Server:     true,
							Addresses: []Address{
								{
									Network:   "dc1",
									IPAddress: "10.0.1.13",
								},
							},
						},
						"dc1-client1": {
							Index:      0,
							Datacenter: "dc1",
							Name:       "dc1-client1",
							Partition:  "default",
							Addresses: []Address{
								{
									Network:   "dc1",
									IPAddress: "10.0.1.21",
								},
							},
							Service: &Service{
								ID:                util.NewIdentifier("ping", "", ""),
								Port:              8080,
								UpstreamID:        util.NewIdentifier("pong", "", ""),
								UpstreamLocalPort: 9090,
								Meta: map[string]string{
									"foo": "bar",
									"RAB": "OOF",
								},
							},
						},
						"dc1-client2": {
							Index:      1,
							Datacenter: "dc1",
							Name:       "dc1-client2",
							Partition:  "default",
							Addresses: []Address{
								{
									Network:   "dc1",
									IPAddress: "10.0.1.22",
								},
							},
							Service: &Service{
								ID:                util.NewIdentifier("pong", "", ""),
								Port:              8080,
								UpstreamID:        util.NewIdentifier("ping", "", ""),
								UpstreamLocalPort: 9090,
								Meta:              map[string]string{},
							},
						},
						"dc1-client3": {
							Index:      2,
							Datacenter: "dc1",
							Name:       "dc1-client3",
							Partition:  "default",
							Addresses: []Address{
								{
									Network:   "dc1",
									IPAddress: "10.0.1.23",
								},
								{
									Network:   "wan",
									IPAddress: "10.1.1.23",
								},
							},
							MeshGateway: true,
						},
						// ============= dc2 ==============
						"dc2-server1": {
							Index:      0,
							Datacenter: "dc2",
							Name:       "dc2-server1",
							Partition:  "default",
							Server:     true,
							Addresses: []Address{
								{
									Network:   "dc2",
									IPAddress: "10.0.2.11",
								},
								{
									Network:   "wan",
									IPAddress: "10.1.2.11",
								},
							},
						},
						"dc2-server2": {
							Index:      1,
							Datacenter: "dc2",
							Name:       "dc2-server2",
							Partition:  "default",
							Server:     true,
							Addresses: []Address{
								{
									Network:   "dc2",
									IPAddress: "10.0.2.12",
								},
								{
									Network:   "wan",
									IPAddress: "10.1.2.12",
								},
							},
						},
						"dc2-server3": {
							Index:      2,
							Datacenter: "dc2",
							Name:       "dc2-server3",
							Partition:  "default",
							Server:     true,
							Addresses: []Address{
								{
									Network:   "dc2",
									IPAddress: "10.0.2.13",
								},
								{
									Network:   "wan",
									IPAddress: "10.1.2.13",
								},
							},
						},
						"dc2-client1": {
							Index:      0,
							Datacenter: "dc2",
							Name:       "dc2-client1",
							Partition:  "default",
							Addresses: []Address{
								{
									Network:   "dc2",
									IPAddress: "10.0.2.21",
								},
							},
							Service: &Service{
								ID:                util.NewIdentifier("ping", "", ""),
								Port:              8080,
								UpstreamID:        util.NewIdentifier("pong", "", ""),
								UpstreamLocalPort: 9090,
								Meta:              map[string]string{},
							},
						},
						"dc2-client2": {
							Index:      1,
							Datacenter: "dc2",
							Name:       "dc2-client2",
							Partition:  "default",
							Addresses: []Address{
								{
									Network:   "dc2",
									IPAddress: "10.0.2.22",
								},
							},
							Service: &Service{
								ID:                 util.NewIdentifier("pong", "", ""),
								Port:               8080,
								UpstreamID:         util.NewIdentifier("blah", "", "also-fake"),
								UpstreamDatacenter: "fake",
								UpstreamExtraHCL:   "// not real",
								UpstreamLocalPort:  9090,
								Meta: map[string]string{
									"AAA": "BBB",
								},
							},
							UseBuiltinProxy: true,
							Canary:          true,
						},
						"dc2-client3": {
							Index:      2,
							Datacenter: "dc2",
							Name:       "dc2-client3",
							Partition:  "default",
							Addresses: []Address{
								{
									Network:   "dc2",
									IPAddress: "10.0.2.23",
								},
								{
									Network:   "wan",
									IPAddress: "10.1.2.23",
								},
							},
							MeshGateway: true,
						},
					},
					servers: []string{
						"dc1-server1",
						"dc1-server2",
						"dc1-server3",
						"dc2-server1",
						"dc2-server2",
						"dc2-server3",
					},
					clients: []string{
						"dc1-client1",
						"dc1-client2",
						"dc1-client3",
						"dc2-client1",
						"dc2-client2",
						"dc2-client3",
					},
					additionalPrimaryGateways: []string(nil),
				}
				require.Equal(t, expect, topo)

				// Check helper methods
				require.Len(t, topo.Networks(), 3)
				require.Len(t, topo.Datacenters(), 2)

				require.Len(t, topo.DatacenterNodes("dc1"), 6)
				require.Len(t, topo.ServerIPs("dc1"), 3)
				require.Len(t, topo.GatewayAddrs("dc1"), 1)

				node1 := topo.Node("dc1-client1")
				require.NotNil(t, node1)
				require.Equal(t, "dc1-client1", node1.Name)

				require.Len(t, topo.DatacenterNodes("dc2"), 6)
				require.Len(t, topo.ServerIPs("dc2"), 3)
				require.Len(t, topo.GatewayAddrs("dc2"), 1)

				node2 := topo.Node("dc2-client2")
				require.NotNil(t, node2)
				require.Equal(t, "dc2-client2", node2.Name)
			},
		},
	}

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			topo, err := CompileTopology(tc.cfg)
			if tc.expectExactErr != "" {
				require.EqualError(t, err, tc.expectExactErr)
				require.Nil(t, topo)
			} else if tc.expectErr {
				require.Error(t, err)
				require.Nil(t, topo)
				// primary datacenter "dc1" is missing from config

			} else {
				require.NoError(t, err)
				require.NotNil(t, topo)
				tc.expectFn(t, topo)
			}
		})
	}
}
