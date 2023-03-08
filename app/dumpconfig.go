package app

import (
	"flag"
	"fmt"

	"github.com/rboyer/devconsul/infra"
)

func (c *Core) RunConfigDump() error {
	args := flag.Args()

	var (
		servers    = make(map[string]int)
		clients    = make(map[string]int)
		dataplanes = make(map[string]int)
		localAddrs = make(map[string]string)
		clusters   []string
		pods       = make(map[string][]string)
		containers = make(map[string][]string)
	)
	c.topology.WalkSilent(func(n *infra.Node) {
		switch n.Kind {
		case infra.NodeKindServer:
			servers[n.Cluster]++
		case infra.NodeKindClient:
			clients[n.Cluster]++
		case infra.NodeKindDataplane:
			dataplanes[n.Cluster]++
		}
		localAddrs[n.Name] = n.LocalAddress()

		pods[n.Cluster] = append(pods[n.Cluster], n.PodName())
		if n.IsAgent() {
			containers[n.Cluster] = append(containers[n.Cluster], n.Name)
		}
		if n.MeshGateway {
			containers[n.Cluster] = append(containers[n.Cluster], n.Name+"-mesh-gateway")
		}
		if n.Kind == infra.NodeKindInfra {
			containers[n.Cluster] = append(containers[n.Cluster], n.Name+"-catalog-sync")
		}

		if n.RunsWorkloads() && n.Service != nil {
			s := n.Service

			containers[n.Cluster] = append(containers[n.Cluster], n.Name+"-"+s.ID.Name)
			containers[n.Cluster] = append(containers[n.Cluster], n.Name+"-"+s.ID.Name+"-sidecar")
		}
	})

	for _, dc := range c.topology.Clusters() {
		clusters = append(clusters, dc.Name)
	}

	m := map[string]interface{}{
		"confName":         c.config.ConfName,
		"image":            c.config.Versions.ConsulImage,
		"envoyVersion":     c.config.Versions.Envoy,
		"tls":              bool2str(c.config.EncryptionTLS),
		"gossip":           bool2str(c.config.EncryptionGossip),
		"k8s":              bool2str(c.config.KubernetesEnabled),
		"acls":             bool2str(!c.config.SecurityDisableACLs),
		"gossipKey":        c.config.GossipKey,
		"agentMasterToken": c.config.AgentMasterToken,
		"localAddrs":       localAddrs,
		"clusters":         clusters,
		"pods":             pods,
		"containers":       containers,
		"linkMode":         c.topology.LinkMode,
	}

	for dc, n := range servers {
		m["topology.servers."+dc] = n
	}
	for dc, n := range clients {
		m["topology.clients."+dc] = n
	}
	for dc, n := range dataplanes {
		m["topology.dataplanes."+dc] = n
	}

	if len(args) == 0 {
		fmt.Printf(jsonPretty(m) + "\n")
		return nil
	}

	v := m[args[0]]
	if v != "" {
		fmt.Println(v)
	}
	return nil
}

func bool2str(b bool) string {
	if b {
		return "1"
	}
	return ""
}
