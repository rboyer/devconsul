package infra

import (
	"sort"

	"github.com/rboyer/devconsul/util"
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

func (t *Topology) HasFederation() bool {
	for _, dc := range t.Datacenters() {
		if !dc.Primary {
			return true
		}
	}
	return false
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
	Partition       string // will be not empty
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
	ID util.Identifier
	// Name               string
	// Namespace          string // will not be empty
	// Partition          string // will be not empty
	Port       int
	UpstreamID util.Identifier
	// UpstreamName       string
	// UpstreamNamespace  string
	// UpstreamPartition  string
	UpstreamDatacenter string
	UpstreamLocalPort  int
	UpstreamExtraHCL   string
	Meta               map[string]string
}
