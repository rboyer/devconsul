package infra

import (
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
		needsAllNetworks = false
	default:
		return nil, fmt.Errorf("unknown network_shape: %s", cfg.TopologyNetworkShape)
	}

	if topology.NetworkShape == NetworkShapeIslands && !cfg.EncryptionTLS {
		return nil, fmt.Errorf("network_shape=%q requires TLS to be enabled to function", topology.NetworkShape)
	}

	if cfg.PrometheusEnabled && topology.NetworkShape != NetworkShapeFlat {
		return nil, fmt.Errorf("enabling prometheus currently requires network_shape=flat")
	}

	canaryConfigured, canaryNodes := cfg.CanaryInfo()

	getDatacenter := func(name string) *config.Datacenter {
		for _, dc := range cfg.TopologyDatacenters {
			if dc.Name == name {
				return dc
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

	forDC := func(dc, baseIP, wanBaseIP string, servers, clients, meshGateways int) error {
		for idx := 1; idx <= servers; idx++ {
			id := strconv.Itoa(idx)
			ip := baseIP + "." + strconv.Itoa(10+idx)
			wanIP := wanBaseIP + "." + strconv.Itoa(10+idx)

			node := &Node{
				Datacenter: dc,
				Name:       dc + "-server" + id,
				Server:     true,
				Partition:  "default",
				Addresses: []Address{
					{
						Network:   topology.NetworkShape.GetNetworkName(dc),
						IPAddress: ip,
					},
				},
				Index: idx - 1,
			}

			switch topology.NetworkShape {
			case NetworkShapeIslands:
				if dc != config.PrimaryDC {
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

		numServiceClients := clients - meshGateways
		for idx := 1; idx <= clients; idx++ {
			isGatewayClient := (idx > numServiceClients)

			id := strconv.Itoa(idx)
			ip := baseIP + "." + strconv.Itoa(20+idx)
			wanIP := wanBaseIP + "." + strconv.Itoa(20+idx)

			nodeName := dc + "-client" + id
			node := &Node{
				Datacenter: dc,
				Name:       nodeName,
				Server:     false,
				Addresses: []Address{
					{
						Network:   topology.NetworkShape.GetNetworkName(dc),
						IPAddress: ip,
					},
				},
				Index: idx - 1,
			}

			nodeConfig := config.Node{} // yay zero value!
			if c := getNode(nodeName); c != nil {
				nodeConfig = *c
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

			if isGatewayClient {
				node.MeshGateway = true

				if node.Partition != "default" {
					return fmt.Errorf("mesh gateways can only be deployed in the default partition")
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
					svc.ID = util.NewIdentifier("ping", nodeConfig.ServiceNamespace, node.Partition)
					svc.UpstreamID = util.NewIdentifier("pong", nodeConfig.UpstreamNamespace, nodeConfig.UpstreamPartition)
					// svc.Name = "ping"
					// svc.UpstreamName = "pong"
				} else {
					svc.ID = util.NewIdentifier("pong", nodeConfig.ServiceNamespace, node.Partition)
					svc.UpstreamID = util.NewIdentifier("ping", nodeConfig.UpstreamNamespace, nodeConfig.UpstreamPartition)
					// svc.Name = "pong"
					// svc.UpstreamName = "ping"
				}

				// svc.Partition = node.Partition
				// svc.UpstreamPartition = nodeConfig.UpstreamPartition

				// svc.Namespace = nodeConfig.ServiceNamespace
				// svc.UpstreamNamespace = nodeConfig.UpstreamNamespace

				if nodeConfig.UpstreamName != "" {
					svc.UpstreamID.Name = nodeConfig.UpstreamName
					// svc.UpstreamName = nodeConfig.UpstreamName
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
				if node.MeshGateway && node.Datacenter == config.PrimaryDC && nodeConfig.RetainInPrimaryGatewaysList {
					topology.AddAdditionalPrimaryGateway(node.PublicAddress() + ":8443")
				}
				continue // act like this isn't there
			}
			topology.AddNode(node)
		}

		return nil
	}

	if dc := getDatacenter(config.PrimaryDC); dc == nil {
		return nil, fmt.Errorf("primary datacenter %q is missing from config", config.PrimaryDC)
	}

	dcPatt := regexp.MustCompile(`^dc([0-9]+)$`)

	for _, dc := range cfg.TopologyDatacenters {
		if dc.MeshGateways < 0 {
			return nil, fmt.Errorf("%s: mesh gateways must be non-negative", dc.Name)
		}
		dc.Clients += dc.MeshGateways // the gateways are just fancy clients

		if dc.Servers <= 0 {
			return nil, fmt.Errorf("%s: must always have at least one server", dc.Name)
		}
		if dc.Clients <= 0 {
			return nil, fmt.Errorf("%s: must always have at least one client", dc.Name)
		}

		m := dcPatt.FindStringSubmatch(dc.Name)
		if m == nil {
			return nil, fmt.Errorf("%s: not a valid datacenter name", dc.Name)
		}
		i, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("%s: not a valid datacenter name", dc.Name)
		}

		thisDC := &Datacenter{
			Name:         dc.Name,
			Primary:      dc.Name == config.PrimaryDC,
			Index:        i,
			Servers:      dc.Servers,
			Clients:      dc.Clients,
			MeshGateways: dc.MeshGateways,
			BaseIP:       fmt.Sprintf("10.0.%d", i),
			WANBaseIP:    fmt.Sprintf("10.1.%d", i),
		}
		topology.dcs = append(topology.dcs, thisDC)

		if needsAllNetworks {
			topology.AddNetwork(&Network{
				Name: thisDC.Name,
				CIDR: thisDC.BaseIP + ".0/24",
			})
		}
	}
	sort.Slice(topology.dcs, func(i, j int) bool {
		return topology.dcs[i].Name < topology.dcs[j].Name
	})

	for _, dc := range topology.dcs {
		err := forDC(dc.Name, dc.BaseIP, dc.WANBaseIP, dc.Servers, dc.Clients, dc.MeshGateways)
		if err != nil {
			return nil, err
		}
	}

	return topology, nil
}
