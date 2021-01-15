package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

type NetworkShape string

const (
	// NetworkShapeIslands describes an isolated island topology where only the
	// mesh gateways are on the WAN.
	NetworkShapeIslands = NetworkShape("islands")

	// NetworkShapeDual describes a private/public lan/wan split where the
	// servers/meshGateways can route to all other servers/meshGateways and the
	// clients are isolated.
	NetworkShapeDual = NetworkShape("dual")

	// NetworkShapeFlat describes a flat network where every agent has a single
	// ip address and they all are routable.
	NetworkShapeFlat = NetworkShape("flat")
)

func (s NetworkShape) GetNetworkName(dc string) string {
	switch s {
	case NetworkShapeIslands, NetworkShapeDual:
		return dc
	case NetworkShapeFlat:
		return "lan"
	default:
		panic("unknown shape: " + s)
	}
}

func InferTopology(uct *userConfigTopology, enterpriseEnabled, canaryConfigured bool) (*Topology, error) {
	topology := &Topology{}

	needsAllNetworks := false
	switch uct.NetworkShape {
	case "islands":
		topology.NetworkShape = NetworkShapeIslands
		topology.DisableWANBootstrap = uct.DisableWANBootstrap
		needsAllNetworks = true
	case "dual":
		topology.NetworkShape = NetworkShapeDual
		needsAllNetworks = true
	case "flat", "":
		topology.NetworkShape = NetworkShapeFlat
		needsAllNetworks = false
	default:
		return nil, fmt.Errorf("unknown network_shape: %s", uct.NetworkShape)
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

	nodeConfigs := uct.NodeConfig

	forDC := func(dc, baseIP, wanBaseIP string, servers, clients, meshGateways int) error {
		for idx := 1; idx <= servers; idx++ {
			id := strconv.Itoa(idx)
			ip := baseIP + "." + strconv.Itoa(10+idx)
			wanIP := wanBaseIP + "." + strconv.Itoa(10+idx)

			node := &Node{
				Datacenter: dc,
				Name:       dc + "-server" + id,
				Server:     true,
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
				if dc != PrimaryDC && !topology.DisableWANBootstrap { // Needed for initial join
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

			nodeConfig := userConfigTopologyNodeConfig{} // yay zero value!
			if nodeConfigs != nil {
				if c, ok := nodeConfigs[nodeName]; ok {
					nodeConfig = c
				}
			}

			if isGatewayClient {
				node.MeshGateway = true

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
					svc.Name = "ping"
					svc.UpstreamName = "pong"
				} else {
					svc.Name = "pong"
					svc.UpstreamName = "ping"
				}

				if nodeConfig.UpstreamName != "" {
					svc.UpstreamName = nodeConfig.UpstreamName
				}
				if nodeConfig.UpstreamDatacenter != "" {
					svc.UpstreamDatacenter = nodeConfig.UpstreamDatacenter
				}
				if nodeConfig.ServiceNamespace != "" {
					if !enterpriseEnabled {
						return fmt.Errorf("namespaces cannot be configured when enterprise.enabled=false")
					}
					svc.Namespace = nodeConfig.ServiceNamespace
				}
				if nodeConfig.UpstreamNamespace != "" {
					if !enterpriseEnabled {
						return fmt.Errorf("namespaces cannot be configured when enterprise.enabled=false")
					}
					svc.UpstreamNamespace = nodeConfig.UpstreamNamespace
				}

				node.Service = &svc
			}

			if nodeConfig.Canary && !canaryConfigured {
				return fmt.Errorf("cannot mark a node as a canary node without configuring canary_proxies section")
			}
			node.Canary = nodeConfig.Canary

			if nodeConfig.Dead {
				if node.MeshGateway && node.Datacenter == PrimaryDC && nodeConfig.RetainInPrimaryGatewaysList {
					topology.AddAdditionalPrimaryGateway(node.PublicAddress() + ":8443")
				}
				continue // act like this isn't there
			}
			topology.AddNode(node)
		}

		return nil
	}

	if _, ok := uct.Datacenters[PrimaryDC]; !ok {
		return nil, fmt.Errorf("primary datacenter %q is missing from config", PrimaryDC)
	}

	dcPatt := regexp.MustCompile(`^dc([0-9]+)$`)

	for dc, v := range uct.Datacenters {
		if v.MeshGateways < 0 {
			return nil, fmt.Errorf("%s: mesh gateways must be non-negative", dc)
		}
		v.Clients += v.MeshGateways // the gateways are just fancy clients

		if v.Servers <= 0 {
			return nil, fmt.Errorf("%s: must always have at least one server", dc)
		}
		if v.Clients <= 0 {
			return nil, fmt.Errorf("%s: must always have at least one client", dc)
		}

		m := dcPatt.FindStringSubmatch(dc)
		if m == nil {
			return nil, fmt.Errorf("%s: not a valid datacenter name", dc)
		}
		i, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("%s: not a valid datacenter name", dc)
		}

		thisDC := &Datacenter{
			Name:         dc,
			Primary:      dc == PrimaryDC,
			Index:        i,
			Servers:      v.Servers,
			Clients:      v.Clients,
			MeshGateways: v.MeshGateways,
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

type Topology struct {
	NetworkShape        NetworkShape
	DisableWANBootstrap bool

	networks map[string]*Network
	dcs      []*Datacenter

	nm      map[string]*Node
	servers []string // node names
	clients []string // node names

	additionalPrimaryGateways []string
}

func (t *Topology) LeaderIP(datacenter string, wan bool) string {
	for _, name := range t.servers {
		n := t.Node(name)
		if n.Datacenter == datacenter {
			if wan {
				return n.PublicAddress()
			} else {
				return n.LocalAddress()
			}
		}
	}
	panic("no such dc")
}

func (t *Topology) Datacenters() []Datacenter {
	out := make([]Datacenter, len(t.dcs))
	for i, dc := range t.dcs {
		out[i] = *dc
	}
	return out
}

func (t *Topology) Networks() []*Network {
	out := make([]*Network, 0, len(t.networks))
	for _, n := range t.networks {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (t *Topology) DC(name string) *Datacenter {
	for _, dc := range t.dcs {
		if dc.Name == name {
			return dc
		}
	}
	panic("no such dc")
}

func (t *Topology) ServerIPs(datacenter string) []string {
	var out []string
	for _, name := range t.servers {
		n := t.Node(name)
		if n.Datacenter == datacenter {
			out = append(out, n.LocalAddress())
		}
	}
	return out
}

func (t *Topology) GatewayAddrs(datacenter string) []string {
	var out []string
	for _, name := range t.clients {
		n := t.Node(name)
		if n.Datacenter == datacenter && n.MeshGateway {
			out = append(out, n.PublicAddress()+":8443")
		}
	}
	out = append(out, t.additionalPrimaryGateways...)
	return out
}

func (t *Topology) all() []string {
	o := make([]string, 0, len(t.servers)+len(t.clients))
	o = append(o, t.servers...)
	o = append(o, t.clients...)
	return o
}

func (t *Topology) Node(name string) *Node {
	if t.nm == nil {
		panic("node not found: " + name)
	}
	n, ok := t.nm[name]
	if !ok {
		panic("node not found: " + name)
	}
	return n
}

func (t *Topology) Nodes() []*Node {
	out := make([]*Node, 0, len(t.nm))
	t.WalkSilent(func(n *Node) {
		out = append(out, n)
	})
	return out
}

func (t *Topology) DatacenterNodes(dc string) []*Node {
	out := make([]*Node, 0, len(t.nm))
	t.WalkSilent(func(n *Node) {
		if n.Datacenter == dc {
			out = append(out, n)
		}
	})
	return out
}

func (t *Topology) Walk(f func(n *Node) error) error {
	for _, nodeName := range t.all() {
		node := t.Node(nodeName)
		if err := f(node); err != nil {
			return err
		}
	}
	return nil
}
func (t *Topology) WalkSilent(f func(n *Node)) {
	for _, nodeName := range t.all() {
		node := t.Node(nodeName)
		f(node)
	}
}

func (t *Topology) AddNetwork(n *Network) {
	if t.networks == nil {
		t.networks = make(map[string]*Network)
	}
	t.networks[n.Name] = n
}

func (t *Topology) AddNode(node *Node) {
	if t.nm == nil {
		t.nm = make(map[string]*Node)
	}
	t.nm[node.Name] = node
	if node.Server {
		t.servers = append(t.servers, node.Name)
	} else {
		t.clients = append(t.clients, node.Name)
	}
}

func (t *Topology) AddAdditionalPrimaryGateway(addr string) {
	t.additionalPrimaryGateways = append(t.additionalPrimaryGateways, addr)
}

type Datacenter struct {
	Name    string
	Primary bool

	Index        int
	Servers      int
	Clients      int
	MeshGateways int

	BaseIP    string
	WANBaseIP string
}

type Network struct {
	Name string
	CIDR string
}

func (n *Network) DockerName() string {
	return "devconsul-" + n.Name
}

type Node struct {
	Datacenter      string
	Name            string
	Server          bool
	Addresses       []Address
	Service         *Service
	MeshGateway     bool
	UseBuiltinProxy bool
	Index           int
	Canary          bool
}

func (n *Node) AddLabels(m map[string]string) {
	m["devconsul.datacenter"] = n.Datacenter

	var agentType string
	if n.Server {
		agentType = "server"
	} else {
		agentType = "client"
	}
	m["devconsul.agentType"] = agentType

	m["devconsul.node"] = n.Name
}

func (n *Node) TokenName() string { return "agent--" + n.Name }

func (n *Node) LocalAddress() string {
	for _, a := range n.Addresses {
		switch a.Network {
		case n.Datacenter, "lan":
			return a.IPAddress
		}
	}
	panic("node has no local address")
}

func (n *Node) PublicAddress() string {
	for _, a := range n.Addresses {
		if a.Network == "wan" {
			return a.IPAddress
		}
	}
	panic("node has no public address")
}

type Address struct {
	Network   string
	IPAddress string
}

type Service struct {
	Name               string
	Namespace          string
	Port               int
	UpstreamName       string
	UpstreamNamespace  string
	UpstreamDatacenter string
	UpstreamLocalPort  int
	UpstreamExtraHCL   string
	Meta               map[string]string
}
