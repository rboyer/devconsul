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

type ClusterLinkMode string

const (
	ClusterLinkModePeer     = ClusterLinkMode("peer")
	ClusterLinkModeFederate = ClusterLinkMode("federate")
)

type NodeMode string

const (
	NodeModeAgent     = NodeMode("agent")
	NodeModeDataplane = NodeMode("dataplane")
)

type Topology struct {
	NetworkShape NetworkShape
	LinkMode     ClusterLinkMode
	NodeMode     NodeMode

	networks map[string]*Network
	clusters []*Cluster

	nm map[string]*Node

	additionalPrimaryGateways []string
}

func (t *Topology) FederateWithGateways() bool { return t.NetworkShape == NetworkShapeIslands }

func (t *Topology) LinkWithFederation() bool { return t.LinkMode == ClusterLinkModeFederate }
func (t *Topology) LinkWithPeering() bool    { return t.LinkMode == ClusterLinkModePeer }

func (t *Topology) LeaderIP(cluster string, wan bool) string {
	for _, name := range t.sortedNodeKind(NodeKindServer) {
		n := t.Node(name)
		if n.Cluster == cluster {
			if wan {
				return n.PublicAddress()
			} else {
				return n.LocalAddress()
			}
		}
	}
	panic("no such dc")
}

func (t *Topology) Clusters() []Cluster {
	out := make([]Cluster, len(t.clusters))
	for i, c := range t.clusters {
		out[i] = *c
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

func (t *Topology) Cluster(name string) *Cluster {
	for _, c := range t.clusters {
		if c.Name == name {
			return c
		}
	}
	panic("no such cluster")
}

func (t *Topology) ServerIPs(cluster string) []string {
	var out []string
	for _, name := range t.sortedNodeKind(NodeKindServer) {
		n := t.Node(name)
		if n.Cluster == cluster {
			out = append(out, n.LocalAddress())
		}
	}
	return out
}

func (t *Topology) GatewayAddrs(cluster string) []string {
	var out []string
	t.WalkSilent(func(n *Node) {
		switch n.Kind {
		case NodeKindClient, NodeKindDataplane:
			if n.Cluster == cluster && n.MeshGateway {
				out = append(out, n.PublicAddress()+":8443")
			}
		}
	})
	out = append(out, t.additionalPrimaryGateways...)
	return out
}

func (t *Topology) sortedNodeKind(kind NodeKind) []string {
	var o []string
	for name, n := range t.nm {
		if n.Kind == kind {
			o = append(o, name)
		}
	}
	sort.Strings(o)
	return o
}

func (t *Topology) sortedNodes() []string {
	var o1, o2, o3 []string
	for name, n := range t.nm {
		switch n.Kind {
		case NodeKindInfra:
			o1 = append(o1, name)
		case NodeKindServer:
			o2 = append(o2, name)
		case NodeKindClient, NodeKindDataplane:
			o3 = append(o3, name)
		}
	}
	sort.Strings(o1)
	sort.Strings(o2)
	sort.Strings(o3)

	o1 = append(o1, o2...)
	o1 = append(o1, o3...)
	return o1
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

func (t *Topology) ClusterNodes(cluster string) []*Node {
	out := make([]*Node, 0, len(t.nm))
	t.WalkSilent(func(n *Node) {
		if n.Cluster == cluster {
			out = append(out, n)
		}
	})
	return out
}

func (t *Topology) Walk(f func(n *Node) error) error {
	for _, nodeName := range t.sortedNodes() {
		node := t.Node(nodeName)
		if err := f(node); err != nil {
			return err
		}
	}
	return nil
}
func (t *Topology) WalkSilent(f func(n *Node)) {
	for _, nodeName := range t.sortedNodes() {
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
	if node.Kind == "" {
		panic("missing node kind")
	}
	if t.nm == nil {
		t.nm = make(map[string]*Node)
	}
	t.nm[node.Name] = node
}

func (t *Topology) AddAdditionalPrimaryGateway(addr string) {
	t.additionalPrimaryGateways = append(t.additionalPrimaryGateways, addr)
}

type Cluster struct {
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

type NodeKind string

const (
	NodeKindUnknown   NodeKind = ""
	NodeKindServer    NodeKind = "server"
	NodeKindClient    NodeKind = "client"
	NodeKindDataplane NodeKind = "dataplane"
	NodeKindInfra     NodeKind = "infra"
)

type Node struct {
	Kind            NodeKind
	Cluster         string
	Name            string
	Segment         string // may be empty
	Partition       string // will be not empty
	Addresses       []Address
	Service         *Service
	MeshGateway     bool
	UseBuiltinProxy bool
	Index           int
	Canary          bool
	// mesh-gateway only
	MeshGatewayUseDNSWANAddress bool
}

func (n *Node) PodName() string  { return n.Name + "-pod" }
func (n *Node) Hostname() string { return n.PodName() }

func (n *Node) IsServer() bool {
	return n.Kind == NodeKindServer
}

func (n *Node) IsAgent() bool {
	switch n.Kind {
	case NodeKindServer, NodeKindClient:
		return true
	}
	return false
}

func (n *Node) RunsWorkloads() bool {
	switch n.Kind {
	case NodeKindServer, NodeKindClient, NodeKindDataplane:
		return true
	}
	return false
}

func (n *Node) AddLabels(m map[string]string) {
	m["devconsul.cluster"] = n.Cluster
	m["devconsul.node"] = n.Name
	m["devconsul.kind"] = string(n.Kind)
}

func (n *Node) TokenName() string { return "agent--" + n.Name }

func (n *Node) LocalAddress() string {
	for _, a := range n.Addresses {
		switch a.Network {
		case n.Cluster, "lan":
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
	Port               int
	UpstreamID         util.Identifier
	UpstreamPeer       string
	UpstreamDatacenter string
	UpstreamLocalPort  int
	UpstreamExtraHCL   string
	Meta               map[string]string
}
