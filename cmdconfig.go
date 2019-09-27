package main

import (
	"flag"
	"fmt"
)

type CommandConfig struct {
	*Core
}

func (c *CommandConfig) Run() error {
	flag.Parse()
	args := flag.Args()

	var (
		servers     = make(map[string]int)
		clients     = make(map[string]int)
		localAddrs  = make(map[string]string)
		datacenters []string
	)
	c.topology.WalkSilent(func(n Node) {
		if n.Server {
			servers[n.Datacenter]++
		} else {
			clients[n.Datacenter]++
		}
		localAddrs[n.Name] = n.LocalAddress()
	})

	for _, dc := range c.topology.Datacenters() {
		datacenters = append(datacenters, dc.Name)
	}

	m := map[string]interface{}{
		"image":            c.config2.ConsulImage,
		"tls":              bool2str(c.config2.EncryptionTLS),
		"gossip":           bool2str(c.config2.EncryptionGossip),
		"k8s":              bool2str(c.config2.KubernetesEnabled),
		"gossipKey":        c.config2.GossipKey,
		"agentMasterToken": c.config2.AgentMasterToken,
		"localAddrs":       localAddrs,
		"datacenters":      datacenters,
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
