package main

import (
	"strconv"
)

func InferTopology(c *Config) (*Topology, error) {
	topology := &Topology{
		nm: make(map[string]Node),
	}

	addNode := func(node Node) {
		topology.nm[node.Name] = node
		if node.Server {
			topology.servers = append(topology.servers, node.Name)
		} else {
			topology.clients = append(topology.clients, node.Name)
		}
	}

	forDC := func(dc, baseIP string, servers, clients int) {
		for idx := 1; idx <= servers; idx++ {
			id := strconv.Itoa(idx)
			ip := baseIP + "." + strconv.Itoa(10+idx)

			node := Node{
				Datacenter: dc,
				Name:       dc + "-server" + id,
				Server:     true,
				IPAddress:  ip,
				Index:      idx - 1,
			}
			addNode(node)
		}

		nodeConfigs := c.Topology.NodeConfig

		for idx := 1; idx <= clients; idx++ {
			id := strconv.Itoa(idx)
			ip := baseIP + "." + strconv.Itoa(20+idx)

			nodeName := dc + "-client" + id
			node := Node{
				Datacenter: dc,
				Name:       nodeName,
				Server:     false,
				IPAddress:  ip,
				Index:      idx - 1,
			}

			nodeConfig := ConfigTopologyNodeConfig{} // yay zero value!
			if nodeConfigs != nil {
				if c, ok := nodeConfigs[nodeName]; ok {
					nodeConfig = c
				}
			}

			if nodeConfig.MeshGateway {
				node.MeshGateway = true
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

				node.Service = &svc
			}

			addNode(node)
		}
	}

	forDC("dc1", "10.0.1", c.Topology.Servers.Datacenter1, c.Topology.Clients.Datacenter1)
	forDC("dc2", "10.0.2", c.Topology.Servers.Datacenter2, c.Topology.Clients.Datacenter2)

	return topology, nil
}

type Topology struct {
	servers []string // node names
	clients []string // node names
	nm      map[string]Node
}

func (t *Topology) LeaderIP(datacenter string) string {
	for _, name := range t.servers {
		n := t.Node(name)
		if n.Datacenter == datacenter {
			return n.IPAddress
		}
	}
	panic("no such dc")
}

func (t *Topology) ServerIPs(datacenter string) []string {
	var out []string
	for _, name := range t.servers {
		n := t.Node(name)
		if n.Datacenter == datacenter {
			out = append(out, n.IPAddress)
		}
	}
	return out
}

func (t *Topology) all() []string {
	o := make([]string, 0, len(t.servers)+len(t.clients))
	o = append(o, t.servers...)
	o = append(o, t.clients...)
	return o
}

func (t *Topology) Node(name string) Node {
	if t.nm == nil {
		panic("node not found: " + name)
	}
	n, ok := t.nm[name]
	if !ok {
		panic("node not found: " + name)
	}
	return n
}

func (t *Topology) Walk(f func(n Node) error) error {
	for _, nodeName := range t.all() {
		node := t.Node(nodeName)
		if err := f(node); err != nil {
			return err
		}
	}
	return nil
}
func (t *Topology) WalkSilent(f func(n Node)) {
	for _, nodeName := range t.all() {
		node := t.Node(nodeName)
		f(node)
	}
}

type Node struct {
	Datacenter      string
	Name            string
	Server          bool
	IPAddress       string
	Service         *Service
	MeshGateway     bool
	UseBuiltinProxy bool
	Index           int
}

func (n *Node) TokenName() string { return "agent--" + n.Name }

type Service struct {
	Name               string
	Port               int
	UpstreamName       string
	UpstreamDatacenter string
	UpstreamLocalPort  int
	UpstreamExtraHCL   string
	Meta               map[string]string
}
