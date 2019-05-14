package main

import (
	"flag"
	"fmt"
	"strconv"
)

func (t *Tool) commandConfig() error {
	flag.Parse()
	args := flag.Args()

	topoConfig := t.config.Topology

	m := map[string]string{
		"image":            t.runtimeConfig.ConsulImage,
		"rawImage":         t.config.ConsulImage,
		"tls":              bool2str(t.config.Encryption.TLS),
		"gossip":           bool2str(t.config.Encryption.Gossip),
		"k8s":              bool2str(t.config.Kubernetes.Enabled),
		"gossipKey":        t.runtimeConfig.GossipKey,
		"agentMasterToken": t.runtimeConfig.AgentMasterToken,

		"topologyServersDatacenter1": int2str(topoConfig.Servers.Datacenter1),
		"topologyServersDatacenter2": int2str(topoConfig.Servers.Datacenter2),
		"topologyClientsDatacenter1": int2str(topoConfig.Clients.Datacenter1),
		"topologyClientsDatacenter2": int2str(topoConfig.Clients.Datacenter2),
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

func int2str(v int) string {
	return strconv.Itoa(v)
}
