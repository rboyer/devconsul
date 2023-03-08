package structs

import (
	"sort"

	"github.com/hashicorp/consul/api"

	"github.com/rboyer/devconsul/util"
)

type CatalogDefinition struct {
	Nodes    []*CatalogNode
	Services []*CatalogService
	Proxies  []*CatalogProxy
}

type CatalogNode struct {
	Node            string            `json:",omitempty"`
	Address         string            `json:",omitempty"`
	TaggedAddresses map[string]string `json:",omitempty"`
	NodeMeta        map[string]string `json:",omitempty"`
	Partition       string            `json:",omitempty"`
}

func (n *CatalogNode) ID() util.Identifier2 {
	return util.NewIdentifier2(n.Node, n.Partition)
}

func (n *CatalogNode) ToAPI(enterprise bool) *api.CatalogRegistration {
	r := &api.CatalogRegistration{
		Node:            n.Node,
		Address:         n.Address,
		TaggedAddresses: n.TaggedAddresses,
		NodeMeta:        n.NodeMeta,
	}
	if enterprise {
		r.Partition = n.Partition
	}
	return r
}

type CatalogService struct {
	Node      string `json:",omitempty"`
	Partition string `json:",omitempty"`
	//
	Service   string            `json:",omitempty"`
	Meta      map[string]string `json:",omitempty"`
	Port      int               `json:",omitempty"`
	Address   string            `json:",omitempty"`
	Namespace string            `json:",omitempty"`
	//
	CheckID  string `json:",omitempty"`
	TCPCheck string `json:",omitempty"`
}

func (s *CatalogService) NodeID() util.Identifier2 {
	return util.NewIdentifier2(s.Node, s.Partition)
}

func (s *CatalogService) ID() util.Identifier {
	return util.NewIdentifier(s.Service, s.Namespace, s.Partition)
}

func (s *CatalogService) ToAPI(enterprise bool) *api.CatalogRegistration {
	r := &api.CatalogRegistration{
		Node:           s.Node,
		SkipNodeUpdate: true,
		Service: &api.AgentService{
			Kind:    api.ServiceKindTypical,
			ID:      s.Service,
			Service: s.Service,
			Meta:    s.Meta,
			Port:    s.Port,
			Address: s.Address,
		},
		Check: &api.AgentCheck{
			CheckID:   s.CheckID,
			Name:      "external sync",
			Type:      "external-sync",
			Status:    "passing", //  TODO
			ServiceID: s.Service,
			Definition: api.HealthCheckDefinition{
				TCP: s.TCPCheck,
			},
			Output: "",
		},
	}
	if enterprise {
		r.Partition = s.Partition
		r.Service.Namespace = s.Namespace
		r.Service.Partition = s.Partition
		r.Check.Namespace = s.Namespace
		r.Check.Partition = s.Partition
	}
	return r
}

type CatalogProxy struct {
	CatalogService
	ProxyDestinationServiceName string                  `json:",omitempty"`
	ProxyLocalServicePort       int                     `json:",omitempty"`
	ProxyUpstreams              []*CatalogProxyUpstream `json:",omitempty"`
}

type CatalogProxyUpstream struct {
	DestinationPartition string `json:",omitempty"`
	DestinationNamespace string `json:",omitempty"`
	DestinationPeer      string `json:",omitempty"`
	DestinationName      string `json:",omitempty"`
	Datacenter           string `json:",omitempty"`
	LocalBindPort        int    `json:",omitempty"`
}

func (p *CatalogProxy) ToAPI(enterprise bool) *api.CatalogRegistration {
	r := p.CatalogService.ToAPI(enterprise)
	r.Service.Kind = api.ServiceKindConnectProxy
	r.Service.Proxy = &api.AgentServiceConnectProxyConfig{
		DestinationServiceName: p.ProxyDestinationServiceName,
		DestinationServiceID:   p.ProxyDestinationServiceName,
		LocalServicePort:       p.ProxyLocalServicePort,
	}
	for _, u := range p.ProxyUpstreams {
		newU := api.Upstream{
			DestinationName: u.DestinationName,
			DestinationPeer: u.DestinationPeer,
			LocalBindPort:   u.LocalBindPort,
			Datacenter:      u.Datacenter,
		}
		if enterprise {
			newU.DestinationNamespace = u.DestinationNamespace
			newU.DestinationPartition = u.DestinationPartition
		}
		r.Service.Proxy.Upstreams = append(r.Service.Proxy.Upstreams, newU)
	}
	return r
}

func (d *CatalogDefinition) Sort() {
	sort.Slice(d.Nodes, func(i, j int) bool {
		a := d.Nodes[i]
		b := d.Nodes[j]

		if a.Partition < b.Partition {
			return true
		} else if a.Partition > b.Partition {
			return false
		}

		return a.Node < b.Node
	})

	sort.Slice(d.Services, func(i, j int) bool {
		a := d.Services[i]
		b := d.Services[j]

		if a.Partition < b.Partition {
			return true
		} else if a.Partition > b.Partition {
			return false
		}

		if a.Node < b.Node {
			return true
		} else if a.Node > b.Node {
			return false
		}

		if a.Namespace < b.Namespace {
			return true
		} else if a.Namespace > b.Namespace {
			return false
		}

		return a.Service < b.Service
	})

	sort.Slice(d.Proxies, func(i, j int) bool {
		a := d.Proxies[i]
		b := d.Proxies[j]

		if a.Partition < b.Partition {
			return true
		} else if a.Partition > b.Partition {
			return false
		}

		if a.Node < b.Node {
			return true
		} else if a.Node > b.Node {
			return false
		}

		if a.Namespace < b.Namespace {
			return true
		} else if a.Namespace > b.Namespace {
			return false
		}

		return a.Service < b.Service
	})
}
