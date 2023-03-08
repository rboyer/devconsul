package app

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
	"github.com/rboyer/devconsul/structs"
	"github.com/rboyer/devconsul/util"
)

func (c *Core) generateCatalogInfo(primaryOnly bool) error {
	genLogger := c.logger.Named("generate-catalog-info")

	for _, cluster := range c.topology.Clusters() {
		if primaryOnly && cluster.Name != config.PrimaryCluster {
			continue
		}

		logger := genLogger.With("cluster", cluster.Name)

		var (
			nodes    = make(map[util.Identifier2]*structs.CatalogNode)
			services = make(map[util.Identifier2]map[util.Identifier]*structs.CatalogService)
			proxies  = make(map[util.Identifier2]map[util.Identifier]*structs.CatalogProxy)
		)
		err := c.topology.Walk(func(n *infra.Node) error {
			consulNodeName := n.PodName()

			if n.Cluster != cluster.Name {
				return nil
			}
			if n.Service == nil {
				return nil
			}

			if n.Kind != infra.NodeKindDataplane {
				return nil
			}

			if n.Service.UpstreamExtraHCL != "" {
				panic("cannot do this")
			}

			var (
				nid      = util.NewIdentifier2(consulNodeName, n.Partition)
				sidApp   = n.Service.ID
				sidProxy = util.NewIdentifier(
					n.Service.ID.Name+"-sidecar-proxy",
					n.Service.ID.Namespace,
					n.Service.ID.Partition,
				)
			)

			if _, ok := nodes[nid]; !ok {
				nodes[nid] = &structs.CatalogNode{
					Node:    consulNodeName,
					Address: n.LocalAddress(),
					// TaggedAddresses map[string]string
					NodeMeta:  map[string]string{"devconsul-virtual": "1"},
					Partition: n.Partition,
				}
				logger.Info("agentless node defined",
					"node", consulNodeName,
					"partition", n.Service.ID.Partition,
				)

				services[nid] = make(map[util.Identifier]*structs.CatalogService)
				proxies[nid] = make(map[util.Identifier]*structs.CatalogProxy)
			}

			// register app on node
			services[nid][sidApp] = &structs.CatalogService{
				Node:      consulNodeName,
				Partition: n.Partition,
				//
				Service:   n.Service.ID.Name,
				Meta:      n.Service.Meta,
				Port:      n.Service.Port,
				Address:   n.LocalAddress(),
				Namespace: n.Service.ID.Namespace,
				//
				CheckID:  n.Service.ID.String(),
				TCPCheck: n.LocalAddress() + ":" + strconv.Itoa(n.Service.Port),
			}

			logger.Info("agentless service defined",
				"service", n.Service.ID.Name,
				"node", consulNodeName,
				"namespace", n.Service.ID.Namespace,
				"partition", n.Service.ID.Partition,
			)

			// register proxy for service
			proxies[nid][sidProxy] = &structs.CatalogProxy{
				CatalogService: structs.CatalogService{
					Node:      consulNodeName,
					Partition: n.Partition,
					//
					Service:   n.Service.ID.Name + "-sidecar-proxy",
					Meta:      n.Service.Meta,
					Port:      20000, // TODO: fake
					Address:   n.LocalAddress(),
					Namespace: n.Service.ID.Namespace,
					//
					CheckID:  n.Service.ID.String(),
					TCPCheck: n.LocalAddress() + ":20000",
				},
				ProxyDestinationServiceName: n.Service.ID.Name,
				ProxyLocalServicePort:       n.Service.Port,
				ProxyUpstreams: []*structs.CatalogProxyUpstream{{
					DestinationName:      n.Service.UpstreamID.Name,
					DestinationNamespace: n.Service.UpstreamID.Namespace,
					DestinationPartition: n.Service.UpstreamID.Partition,
					DestinationPeer:      n.Service.UpstreamPeer,
					LocalBindPort:        n.Service.UpstreamLocalPort,
					Datacenter:           n.Service.UpstreamDatacenter,
				}},
			}

			logger.Info("agentless proxy defined",
				"service", n.Service.ID.Name+"-sidecar-proxy",
				"node", consulNodeName,
				"namespace", n.Service.ID.Namespace,
				"namespace", n.Service.ID.Namespace,
				"partition", n.Service.ID.Partition,
			)

			return nil
		})
		if err != nil {
			return fmt.Errorf("error generating catalog info details for cluster %q: %w", cluster.Name, err)
		}

		out := &structs.CatalogDefinition{}
		for _, n := range nodes {
			out.Nodes = append(out.Nodes, n)
		}
		for _, m := range services {
			for _, svc := range m {
				out.Services = append(out.Services, svc)
			}
		}
		for _, m := range proxies {
			for _, p := range m {
				out.Proxies = append(out.Proxies, p)
			}
		}
		out.Sort()

		d, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("error rendering catalog definition for cluster %q: %w", cluster.Name, err)
		}

		filename := "catalog_def." + cluster.Name + ".json"
		if err := c.cache.WriteStringFile(filename, string(d)); err != nil {
			return fmt.Errorf("error writing catalog definition for cluster %q: %w", cluster.Name, err)
		}
	}

	return nil
}
