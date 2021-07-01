package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInferTopology(t *testing.T) {
	type testcase struct {
		uc             *userConfigTopology
		enterprise     bool
		expectErr      bool
		expectExactErr string
		expectFn       func(t *testing.T, topo *Topology)
	}

	cases := map[string]testcase{
		"missing-primary": {
			uc: &userConfigTopology{
				NetworkShape: "flat",
				Datacenters: map[string]*userConfigTopologyDatacenter{
					"dc2": {
						Servers: 1,
						Clients: 1,
					},
				},
			},
			expectExactErr: `primary datacenter "dc1" is missing from config`,
		},
		"full-islands": {
			uc: &userConfigTopology{
				NetworkShape: "islands",
				Datacenters: map[string]*userConfigTopologyDatacenter{
					"dc1": {
						Servers:      3,
						Clients:      2,
						MeshGateways: 1,
					},
					"dc2": {
						Servers:      3,
						Clients:      2,
						MeshGateways: 1,
					},
				},
				NodeConfig: map[string]*userConfigTopologyNodeConfig{
					"dc1-client1": {
						ServiceMeta: map[string]string{
							"foo": "bar",
							"RAB": "OOF",
						},
					},
					"dc2-client2": {
						UpstreamName:       "blah",
						UpstreamDatacenter: "fake",
						UpstreamExtraHCL:   "// not real",
						ServiceMeta: map[string]string{
							"AAA": "BBB",
						},
						UseBuiltinProxy: true,
					},
				},
			},
			expectFn: func(t *testing.T, topo *Topology) {
				require.Equal(t, NetworkShapeIslands, topo.NetworkShape)
				require.Len(t, topo.Networks(), 3)
				require.Len(t, topo.Datacenters(), 2)

				require.Len(t, topo.DatacenterNodes("dc1"), 6)
				require.Len(t, topo.ServerIPs("dc1"), 3)
				require.Len(t, topo.GatewayAddrs("dc1"), 1)

				node1 := topo.Node("dc1-client1")
				require.NotNil(t, node1)
				require.False(t, node1.UseBuiltinProxy)
				require.ElementsMatch(t, []Address{
					{Network: "dc1", IPAddress: "10.0.1.21"},
				}, node1.Addresses)
				require.NotNil(t, node1.Service)
				require.Equal(t, map[string]string{
					"foo": "bar",
					"RAB": "OOF",
				}, node1.Service.Meta)
				require.Equal(t, "pong", node1.Service.UpstreamName)
				require.Equal(t, "", node1.Service.UpstreamDatacenter)
				require.Equal(t, "", node1.Service.UpstreamExtraHCL)

				require.Len(t, topo.DatacenterNodes("dc2"), 6)
				require.Len(t, topo.ServerIPs("dc2"), 3)
				require.Len(t, topo.GatewayAddrs("dc2"), 1)

				node2 := topo.Node("dc2-client2")
				require.NotNil(t, node2)
				require.True(t, node2.UseBuiltinProxy)
				require.ElementsMatch(t, []Address{
					{Network: "dc2", IPAddress: "10.0.2.22"},
				}, node2.Addresses)
				require.NotNil(t, node2.Service)
				require.Equal(t, map[string]string{
					"AAA": "BBB",
				}, node2.Service.Meta)
				require.Equal(t, "blah", node2.Service.UpstreamName)
				require.Equal(t, "fake", node2.Service.UpstreamDatacenter)
				require.Equal(t, "// not real", node2.Service.UpstreamExtraHCL)
			},
		},
	}

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			_ = tc
			topo, err := InferTopology(tc.uc, tc.enterprise, false)
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
