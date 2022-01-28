package main

import (
	"flag"
	"fmt"

	"github.com/rboyer/devconsul/infra"
)

func (c *Core) RunConfigDump() error {
	args := flag.Args()

	var (
		servers     = make(map[string]int)
		clients     = make(map[string]int)
		localAddrs  = make(map[string]string)
		datacenters []string
		pods        = make(map[string][]string)
		containers  = make(map[string][]string)
	)
	c.topology.WalkSilent(func(n *infra.Node) {
		if n.Server {
			servers[n.Datacenter]++
		} else {
			clients[n.Datacenter]++
		}
		localAddrs[n.Name] = n.LocalAddress()

		pods[n.Datacenter] = append(pods[n.Datacenter], n.Name+"-pod")
		containers[n.Datacenter] = append(containers[n.Datacenter], n.Name)
		if n.MeshGateway {
			containers[n.Datacenter] = append(containers[n.Datacenter], n.Name+"-mesh-gateway")
		}
	})

	for _, dc := range c.topology.Datacenters() {
		datacenters = append(datacenters, dc.Name)
	}

	m := map[string]interface{}{
		"image":            c.config.ConsulImage,
		"envoyVersion":     c.config.EnvoyVersion,
		"tls":              bool2str(c.config.EncryptionTLS),
		"gossip":           bool2str(c.config.EncryptionGossip),
		"k8s":              bool2str(c.config.KubernetesEnabled),
		"gossipKey":        c.config.GossipKey,
		"agentMasterToken": c.config.AgentMasterToken,
		"localAddrs":       localAddrs,
		"datacenters":      datacenters,
		"pods":             pods,
		"containers":       containers,
	}

	for dc, n := range servers {
		m["topology.servers."+dc] = n
	}
	for dc, n := range clients {
		m["topology.clients."+dc] = n
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
