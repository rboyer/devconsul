package infra

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/util"
)

// CompileTopology creates a Topology based on the provided configuration.
func CompileTopology(cfg *config.Config) (*Topology, error) {
	var (
		topology         = &Topology{}
		needsAllNetworks = false
	)
	switch cfg.TopologyNetworkShape {
	case "islands":
		topology.NetworkShape = NetworkShapeIslands
		needsAllNetworks = true
	case "dual":
		topology.NetworkShape = NetworkShapeDual
		needsAllNetworks = true
	case "flat", "":
		topology.NetworkShape = NetworkShapeFlat
	default:
		return nil, fmt.Errorf("unknown network_shape: %s", cfg.TopologyNetworkShape)
	}

	switch cfg.TopologyNodeMode {
	case string(NodeModeAgent):
		topology.NodeMode = NodeModeAgent
	case string(NodeModeDataplane):
		topology.NodeMode = NodeModeDataplane
	default:
		return nil, fmt.Errorf("unknown node_mode: %s", cfg.TopologyNodeMode)
	}

	for _, n := range cfg.TopologyNodes {
		switch n.Mode {
		case string(NodeModeAgent), string(NodeModeDataplane), "":
		default:
			return nil, fmt.Errorf("unknown node[%q].mode: %s", n.NodeName, n.Mode)
		}
	}

	// TODO(peering): require TLS for peering
	switch cfg.TopologyLinkMode {
	case string(ClusterLinkModePeer):
		topology.LinkMode = ClusterLinkModePeer
	case string(ClusterLinkModeFederate):
		topology.LinkMode = ClusterLinkModeFederate
	default:
		return nil, fmt.Errorf("unknown link_mode: %s", cfg.TopologyLinkMode)
	}

	if topology.NetworkShape != NetworkShapeFlat && topology.LinkMode == ClusterLinkModePeer {
		return nil, fmt.Errorf("network_shape=%q is incompatible with link_mode=%q", topology.NetworkShape, topology.LinkMode)
	}

	if topology.NetworkShape == NetworkShapeIslands && !cfg.EncryptionTLS {
		return nil, fmt.Errorf("network_shape=%q requires TLS to be enabled to function", topology.NetworkShape)
	}

	if cfg.PrometheusEnabled && topology.NetworkShape != NetworkShapeFlat {
		return nil, fmt.Errorf("enabling prometheus currently requires network_shape=flat")
	}

	canaryConfigured, canaryNodes := cfg.CanaryInfo()

	getCluster := func(name string) *config.Cluster {
		for _, c := range cfg.TopologyClusters {
			if c.Name == name {
				return c
			}
		}
		return nil
	}

	getNode := func(name string) *config.Node {
		for _, n := range cfg.TopologyNodes {
			if n.NodeName == name {
				return n
			}
		}
		return nil
	}

	if needsAllNetworks {
		topology.AddNetwork(&Network{
			Name: "wan",
			CIDR: "10.1.0.0/16",
		})
	} else {
		topology.AddNetwork(&Network{
			Name: "lan",
			CIDR: "10.0.0.0/16",
		})
	}

	forCluster := func(clusterName, baseIP, wanBaseIP string, servers, clients, meshGateways int) error {
		for idx := 1; idx <= servers; idx++ {
			id := strconv.Itoa(idx)
			ip := baseIP + "." + strconv.Itoa(10+idx)
			wanIP := wanBaseIP + "." + strconv.Itoa(10+idx)

			node := &Node{
				Cluster:   clusterName,
				Kind:      NodeKindServer,
				Name:      clusterName + "-server" + id,
				Partition: "default",
				Addresses: []Address{
					{
						Network:   topology.NetworkShape.GetNetworkName(clusterName),
						IPAddress: ip,
					},
				},
				Index: idx - 1,
			}

			if c := getNode(node.Name); c != nil {
				if c.Mode != string(NodeModeAgent) {
					return fmt.Errorf("a consul server cannot be agentless")
				}
			}

			switch topology.NetworkShape {
			case NetworkShapeIslands:
				if clusterName != config.PrimaryCluster {
					node.Addresses = append(node.Addresses, Address{
						Network:   "wan",
						IPAddress: wanIP,
					})
				}
			case NetworkShapeDual:
				node.Addresses = append(node.Addresses, Address{
					Network:   "wan",
					IPAddress: wanIP,
				})
			case NetworkShapeFlat:
			default:
				return fmt.Errorf("unknown shape: %s", topology.NetworkShape)
			}
			topology.AddNode(node)
		}

		{ // add special pod
			const idx = 100
			ip := baseIP + ".100"

			nodeName := clusterName + "-infra1"
			node := &Node{
				Cluster:   clusterName,
				Name:      nodeName,
				Kind:      NodeKindInfra,
				Partition: "default",
				Addresses: []Address{{
					Network:   topology.NetworkShape.GetNetworkName(clusterName),
					IPAddress: ip,
				}},
				Index: idx - 1,
			}
			topology.AddNode(node)
		}

		numServiceClients := clients - meshGateways
		for idx := 1; idx <= clients; idx++ {
			isGatewayClient := (idx > numServiceClients)

			id := strconv.Itoa(idx)
			ip := baseIP + "." + strconv.Itoa(20+idx)
			wanIP := wanBaseIP + "." + strconv.Itoa(20+idx)

			nodeName := clusterName + "-client" + id
			node := &Node{
				Cluster: clusterName,
				Name:    nodeName,
				Kind:    NodeKindClient,
				Addresses: []Address{
					{
						Network:   topology.NetworkShape.GetNetworkName(clusterName),
						IPAddress: ip,
					},
				},
				Index: idx - 1,
			}

			nodeConfig := config.Node{} // yay zero value!
			if c := getNode(nodeName); c != nil {
				nodeConfig = *c
			}

			if topology.NodeMode == NodeModeDataplane {
				node.Kind = NodeKindDataplane
			}
			switch nodeConfig.Mode {
			case string(NodeModeAgent):
				node.Kind = NodeKindClient
			case string(NodeModeDataplane):
				node.Kind = NodeKindDataplane
			}

			{ // handle partition defaulting regardless of OSS/ENT
				nodeConfig.Partition = util.PartitionOrDefault(nodeConfig.Partition)
				node.Partition = nodeConfig.Partition
				if nodeConfig.UpstreamPartition == "" {
					nodeConfig.UpstreamPartition = nodeConfig.Partition
				}
			}

			{ // handle namespace defaulting regardless of OSS/ENT
				nodeConfig.ServiceNamespace = util.NamespaceOrDefault(nodeConfig.ServiceNamespace)
				if nodeConfig.UpstreamNamespace == "" {
					nodeConfig.UpstreamNamespace = nodeConfig.ServiceNamespace
				}
			}

			node.Segment = nodeConfig.Segment

			if isGatewayClient {
				node.MeshGateway = true

				if node.Partition != "default" {
					return fmt.Errorf("mesh gateways can only be deployed in the default partition")
				}

				if nodeConfig.UseDNSWANAddress {
					if topology.NetworkShape != NetworkShapeFlat {
						return fmt.Errorf("use_dns_wan_address only applies to flat networking models")
					}
					node.MeshGatewayUseDNSWANAddress = nodeConfig.UseDNSWANAddress
				}

				switch topology.NetworkShape {
				case NetworkShapeIslands, NetworkShapeDual:
					node.Addresses = append(node.Addresses, Address{
						Network:   "wan",
						IPAddress: wanIP,
					})
				case NetworkShapeFlat:
				default:
					return fmt.Errorf("unknown shape: %s", topology.NetworkShape)
				}
			} else {
				if nodeConfig.UseDNSWANAddress {
					return fmt.Errorf("use_dns_wan_address only applies to mesh gateways")
				}
				if nodeConfig.UseBuiltinProxy {
					node.UseBuiltinProxy = true
				}
				svc := Service{
					Port:              8080,
					UpstreamLocalPort: 9090,
					UpstreamExtraHCL:  nodeConfig.UpstreamExtraHCL,
					Meta:              nodeConfig.Meta(),
				}
				if idx%2 == 1 {
					svc.ID = util.NewIdentifier(config.ServicePing, nodeConfig.ServiceNamespace, node.Partition)
					svc.UpstreamID = util.NewIdentifier(config.ServicePong, nodeConfig.UpstreamNamespace, nodeConfig.UpstreamPartition)
				} else {
					svc.ID = util.NewIdentifier(config.ServicePong, nodeConfig.ServiceNamespace, node.Partition)
					svc.UpstreamID = util.NewIdentifier(config.ServicePing, nodeConfig.UpstreamNamespace, nodeConfig.UpstreamPartition)
				}

				if nodeConfig.UpstreamName != "" {
					svc.UpstreamID.Name = nodeConfig.UpstreamName
				}
				if nodeConfig.UpstreamPeer != "" {
					svc.UpstreamPeer = nodeConfig.UpstreamPeer
				}
				if nodeConfig.UpstreamDatacenter != "" {
					svc.UpstreamDatacenter = nodeConfig.UpstreamDatacenter
				}

				node.Service = &svc
			}

			if canaryConfigured {
				_, node.Canary = canaryNodes[nodeName]
			}

			if nodeConfig.Dead {
				if cfg.TopologyLinkMode == "federate" {
					if node.MeshGateway && node.Cluster == config.PrimaryCluster && nodeConfig.RetainInPrimaryGatewaysList {
						topology.AddAdditionalPrimaryGateway(node.PublicAddress() + ":8443")
					}
				}
				continue // act like this isn't there
			}
			topology.AddNode(node)
		}

		return nil
	}

	if c := getCluster(config.PrimaryCluster); c == nil {
		return nil, fmt.Errorf("primary cluster %q is missing from config", config.PrimaryCluster)
	}

	clusterNamePatt := regexp.MustCompile(`^dc([0-9]+)$`)

	for _, c := range cfg.TopologyClusters {
		if c.MeshGateways < 0 {
			return nil, fmt.Errorf("%s: mesh gateways must be non-negative", c.Name)
		}
		c.Clients += c.MeshGateways // the gateways are just fancy clients

		if c.Servers <= 0 {
			return nil, fmt.Errorf("%s: must always have at least one server", c.Name)
		}
		if c.Clients <= 0 {
			return nil, fmt.Errorf("%s: must always have at least one client", c.Name)
		}
		if c.Clients > 50 {
			return nil, fmt.Errorf("%s: must always have no more than 50 clients", c.Name)
		}

		m := clusterNamePatt.FindStringSubmatch(c.Name)
		if m == nil {
			return nil, fmt.Errorf("%s: not a valid cluster name", c.Name)
		}
		i, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("%s: not a valid cluster name", c.Name)
		}

		thisCluster := &Cluster{
			Name:         c.Name,
			Index:        i,
			Servers:      c.Servers,
			Clients:      c.Clients,
			MeshGateways: c.MeshGateways,
			BaseIP:       fmt.Sprintf("10.0.%d", i),
			WANBaseIP:    fmt.Sprintf("10.1.%d", i),
		}
		if cfg.TopologyLinkMode == string(ClusterLinkModePeer) {
			thisCluster.Primary = true
		} else if c.Name == config.PrimaryCluster {
			thisCluster.Primary = true
		}
		topology.clusters = append(topology.clusters, thisCluster)

		if needsAllNetworks {
			topology.AddNetwork(&Network{
				Name: thisCluster.Name,
				CIDR: thisCluster.BaseIP + ".0/24",
			})
		}
	}
	sort.Slice(topology.clusters, func(i, j int) bool {
		return topology.clusters[i].Name < topology.clusters[j].Name
	})

	for _, cluster := range topology.clusters {
		err := forCluster(cluster.Name, cluster.BaseIP, cluster.WANBaseIP, cluster.Servers, cluster.Clients, cluster.MeshGateways)
		if err != nil {
			return nil, err
		}
	}

	if err := checkForErrors(topology); err != nil {
		return nil, err
	}

	return topology, nil
}

func checkForErrors(topology *Topology) error {
	return topology.Walk(func(node *Node) error {
		if node.Service == nil {
			return nil
		}
		svc := node.Service

		switch svc.ID.Name {
		case config.ServicePing, config.ServicePong:
			return nil
		default:
			return errors.New("unexpected service: " + svc.ID.Name)
		}
	})
}
