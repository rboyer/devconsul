package tfgen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

func GenerateAgentHCL(
	cfg *config.Config,
	topology *infra.Topology,
	node *infra.Node,
) (string, error) {
	if node.Server {
		return GenerateConsulServerHCL(cfg, topology, node)
	}
	return GenerateConsulClientHCL(cfg, topology, node)
}

func GenerateConsulServerHCL(
	cfg *config.Config,
	topology *infra.Topology,
	node *infra.Node,
) (string, error) {
	if !node.Server {
		panic("wrong plumbing")
	}

	useWANIP := false
	switch topology.NetworkShape {
	case infra.NetworkShapeIslands:
		if node.MeshGateway {
			useWANIP = true
		}
	case infra.NetworkShapeDual:
		useWANIP = true
	case infra.NetworkShapeFlat:
	default:
		panic("unknown shape: " + topology.NetworkShape)
	}

	var inSecondaryDatacenter bool
	if topology.LinkWithFederation() {
		inSecondaryDatacenter = node.Cluster != config.PrimaryCluster
	}

	var b HCLBuilder
	b.add("server", true)
	b.add("bootstrap_expect", len(topology.ServerIPs(node.Cluster)))
	b.add("client_addr", "0.0.0.0")
	b.add("advertise_addr", node.LocalAddress())
	b.add("translate_wan_addrs", true)
	b.add("client_addr", "0.0.0.0")
	b.add("datacenter", node.Cluster)
	b.add("disable_update_check", true)
	b.add("log_level", "trace")
	b.add("enable_debug", true)
	b.add("use_streaming_backend", true)
	b.addBlock("rpc", func() {
		b.add("enable_streaming", true)
	})
	if useWANIP {
		b.add("advertise_addr_wan", node.PublicAddress())
	}
	b.addSlice("retry_join", topology.ServerIPs(node.Cluster))

	if topology.LinkWithFederation() {
		if topology.FederateWithGateways() {
			if node.Cluster != config.PrimaryCluster {
				primaryGateways := topology.GatewayAddrs(config.PrimaryCluster)
				b.addSlice("primary_gateways", primaryGateways)
				b.add("primary_gateways_interval", "5s")
			}
		} else {
			var ips []string
			for _, cluster := range topology.Clusters() {
				ips = append(ips, topology.LeaderIP(cluster.Name, useWANIP))
			}
			b.addSlice("retry_join_wan", ips)
		}
	}

	if topology.LinkWithPeering() {
		b.add("primary_datacenter", node.Cluster)
		b.addBlock("peering", func() {
			b.add("enabled", true)
		})
	} else {
		b.add("primary_datacenter", config.PrimaryCluster)
	}

	b.addBlock("ui_config", func() {
		b.add("enabled", true)
		if cfg.PrometheusEnabled {
			b.add("metrics_provider", "prometheus")
			b.addBlock("metrics_proxy", func() {
				b.add("base_url", "http://prometheus:9090")
			})
		}
	})

	b.addBlock("telemetry", func() {
		b.add("disable_hostname", true)
		b.add("prometheus_retention_time", "168h")
	})

	if len(cfg.EnterpriseSegments) > 0 {
		type networkSegment struct {
			Name string
			Port int
		}
		var sorted []networkSegment
		for name, port := range cfg.EnterpriseSegments {
			sorted = append(sorted, networkSegment{Name: name, Port: port})
		}
		sort.Slice(sorted, func(i, j int) bool {
			a := sorted[i]
			b := sorted[j]
			return a.Port < b.Port
		})
		b.format("segments = [")
		for _, seg := range sorted {
			b.format("{ name = %q port = %d },", seg.Name, seg.Port)
		}
		b.format("]")
	}

	b.add("license_path", "/license.hclic")
	b.add("encrypt", cfg.GossipKey)

	if cfg.EncryptionTLS {
		prefix := node.Cluster + "-server-consul-" + strconv.Itoa(node.Index)
		b.addBlock("tls", func() {
			b.addBlock("internal_rpc", func() {
				b.add("ca_file", "/tls/consul-agent-ca.pem")
				b.add("cert_file", "/tls/"+prefix+".pem")
				b.add("key_file", "/tls/"+prefix+"-key.pem")
				b.add("verify_incoming", true)
				b.add("verify_server_hostname", true)
				b.add("verify_outgoing", true)
			})
			if cfg.EncryptionTLSAPI {
				b.addBlock("https", func() {
					b.add("ca_file", "/tls/consul-agent-ca.pem")
					b.add("cert_file", "/tls/"+prefix+".pem")
					b.add("key_file", "/tls/"+prefix+"-key.pem")
					b.add("verify_incoming", true)
				})
				b.addBlock("grpc", func() {
					b.add("ca_file", "/tls/consul-agent-ca.pem")
					b.add("cert_file", "/tls/"+prefix+".pem")
					b.add("key_file", "/tls/"+prefix+"-key.pem")
					b.add("verify_incoming", true)
				})
			}
		})
	}

	if !inSecondaryDatacenter {
		// Exercise config entry bootstrap
		b.addBlock("config_entries", func() {
			b.addBlock("bootstrap", func() {
				b.add("kind", "service-defaults")
				b.add("name", "placeholder")
				b.add("protocol", "grpc")
			})
			b.addBlock("bootstrap", func() {
				b.add("kind", "service-intentions")
				b.add("name", "placeholder")
				b.addBlock("sources", func() {
					b.add("name", "placeholder-client")
					b.add("action", "allow")
				})
			})
		})
	}

	b.addBlock("connect", func() {
		b.add("enabled", true)
		if topology.FederateWithGateways() {
			b.add("enable_mesh_gateway_wan_federation", true)
		}
	})

	b.addBlock("ports", func() {
		b.add("grpc", 8502)
		if cfg.EncryptionTLSAPI {
			b.add("https", 8501)
		}
	})

	if !cfg.SecurityDisableACLs {
		b.addBlock("acl", func() {
			b.add("enabled", true)
			b.add("default_policy", "deny")
			b.add("down_policy", "extend-cache")
			b.add("enable_token_persistence", true)
			if inSecondaryDatacenter {
				b.add("enable_token_replication", true)
			}
			b.addBlock("tokens", func() {
				if !inSecondaryDatacenter {
					b.add("initial_management", cfg.InitialMasterToken)
				}
				b.add("agent_recovery", cfg.AgentMasterToken)
			})
		})
	}

	return b.String(), nil
}

func GenerateConsulClientHCL(
	cfg *config.Config,
	topology *infra.Topology,
	node *infra.Node,
) (string, error) {
	if node.Server {
		panic("wrong plumbing")
	}

	var b HCLBuilder
	b.add("server", false)
	b.add("client_addr", "0.0.0.0")
	b.add("advertise_addr", node.LocalAddress())
	b.add("client_addr", "0.0.0.0")
	b.add("datacenter", node.Cluster)
	b.add("disable_update_check", true)
	b.add("log_level", "trace")
	b.add("enable_debug", true)
	b.add("use_streaming_backend", true)
	b.addSlice("retry_join", topology.ServerIPs(node.Cluster))

	if topology.LinkWithPeering() {
		b.add("primary_datacenter", node.Cluster)
	} else {
		b.add("primary_datacenter", config.PrimaryCluster)
	}

	b.addBlock("ui_config", func() {
		b.add("enabled", true)
		if cfg.PrometheusEnabled {
			b.add("metrics_provider", "prometheus")
			b.addBlock("metrics_proxy", func() {
				b.add("base_url", "http://prometheus:9090")
			})
		}
	})

	b.addBlock("telemetry", func() {
		b.add("disable_hostname", true)
		b.add("prometheus_retention_time", "168h")
	})

	b.add("segment", node.Segment)
	if !cfg.EnterpriseDisablePartitions && cfg.EnterpriseEnabled {
		b.add("partition", node.Partition)
	}

	b.add("license_path", "/license.hclic")
	b.add("encrypt", cfg.GossipKey)

	if cfg.EncryptionTLS {
		prefix := node.Cluster + "-client-consul-" + strconv.Itoa(node.Index)
		b.addBlock("tls", func() {
			b.addBlock("internal_rpc", func() {
				b.add("ca_file", "/tls/consul-agent-ca.pem")
				b.add("cert_file", "/tls/"+prefix+".pem")
				b.add("key_file", "/tls/"+prefix+"-key.pem")
				b.add("verify_incoming", true)
				b.add("verify_server_hostname", true)
				b.add("verify_outgoing", true)
			})
			if cfg.EncryptionTLSAPI {
				b.addBlock("https", func() {
					b.add("ca_file", "/tls/consul-agent-ca.pem")
					b.add("cert_file", "/tls/"+prefix+".pem")
					b.add("key_file", "/tls/"+prefix+"-key.pem")
					b.add("verify_incoming", true)
				})
				b.addBlock("grpc", func() {
					b.add("ca_file", "/tls/consul-agent-ca.pem")
					b.add("cert_file", "/tls/"+prefix+".pem")
					b.add("key_file", "/tls/"+prefix+"-key.pem")
					b.add("verify_incoming", true)
				})
			}
		})
	}

	b.addBlock("ports", func() {
		b.add("grpc", 8502)
		if cfg.EncryptionTLSAPI {
			b.add("https", 8501)
		}
		if node.Segment != "" {
			port := cfg.EnterpriseSegments[node.Segment]
			b.add("serf_lan", port)
		}
	})

	if !cfg.SecurityDisableACLs {
		b.addBlock("acl", func() {
			b.add("enabled", true)
			b.add("default_policy", "deny")
			b.add("down_policy", "extend-cache")
			b.add("enable_token_persistence", true)
			b.addBlock("tokens", func() {
				b.add("agent_recovery", cfg.AgentMasterToken)
			})
		})
	}

	return b.String(), nil
}

type HCLBuilder struct {
	parts []string
}

func (b *HCLBuilder) format(s string, a ...any) {
	if len(a) == 0 {
		b.parts = append(b.parts, s)
	} else {
		b.parts = append(b.parts, fmt.Sprintf(s, a...))
	}
}

func (b *HCLBuilder) add(k string, v any) {
	switch x := v.(type) {
	case string:
		if x != "" {
			b.format("%s = %q", k, x)
		}
	case int:
		b.format("%s = %d", k, x)
	case bool:
		b.format("%s = %v", k, x)
	default:
		panic(fmt.Sprintf("unexpected type %T", v))
	}
}

func (b *HCLBuilder) addBlock(block string, fn func()) {
	b.format(block + "{")
	fn()
	b.format("}")
}

func (b *HCLBuilder) addSlice(name string, vals []string) {
	b.format(name + " = [")
	for _, v := range vals {
		b.format("%q,", v)
	}
	b.format("]")
}

func (b *HCLBuilder) String() string {
	joined := strings.Join(b.parts, "\n")
	// Ensure it looks tidy
	return string(hclwrite.Format([]byte(joined)))
}
