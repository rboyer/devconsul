package tfgen

import (
	"sort"
	"strings"
	"text/template"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

type pingpongInfo struct {
	PodName            string
	NodeName           string
	PingPong           string // ping or pong
	MetaString         string
	SidecarBootEnvVars []string
	UseBuiltinProxy    bool
	EnvoyLogLevel      string
	EnvoyImageResource string
	EnableACLs         bool
}

func GeneratePingPongContainers(
	config *config.Config,
	podName string,
	node *infra.Node,
) []Resource {
	if node.Service == nil {
		return nil
	}
	svc := node.Service

	ppi := pingpongInfo{
		PodName:            podName,
		NodeName:           node.Name,
		PingPong:           svc.ID.Name,
		UseBuiltinProxy:    node.UseBuiltinProxy,
		EnvoyLogLevel:      config.EnvoyLogLevel,
		EnvoyImageResource: "docker_image.consul-envoy.latest",
		EnableACLs:         !config.SecurityDisableACLs,
	}
	if node.Canary {
		ppi.EnvoyImageResource = "docker_image.consul-envoy-canary.latest"
	}
	if len(svc.Meta) > 0 {
		var kvs []struct{ K, V string }
		for k, v := range svc.Meta {
			kvs = append(kvs, struct{ K, V string }{k, v})
		}
		sort.Slice(kvs, func(i, j int) bool {
			return kvs[i].K < kvs[j].K
		})
		var parts []string
		for _, kv := range kvs {
			parts = append(parts, kv.K+"-"+kv.V)
		}
		ppi.MetaString = strings.Join(parts, "--")
	}

	proxyType := "envoy"
	if node.UseBuiltinProxy {
		proxyType = "builtin"
	}

	if config.SecurityDisableACLs {
		ppi.SidecarBootEnvVars = []string{
			"SBOOT_READY_FILE=/secrets/ready.val",
			"SBOOT_PROXY_TYPE=" + proxyType,
			"SBOOT_REGISTER_FILE=/secrets/servicereg__" + node.Name + "__" + svc.ID.Name + ".hcl",
			//
			"SBOOT_MODE=insecure",
		}
	} else if config.KubernetesEnabled {
		ppi.SidecarBootEnvVars = []string{
			"SBOOT_READY_FILE=/secrets/ready.val",
			"SBOOT_PROXY_TYPE=" + proxyType,
			"SBOOT_REGISTER_FILE=/secrets/servicereg__" + node.Name + "__" + svc.ID.Name + ".hcl",
			//
			"SBOOT_MODE=login",
			"SBOOT_BEARER_TOKEN_FILE=/secrets/k8s/service_jwt_token." + svc.ID.Name,
			"SBOOT_TOKEN_SINK_FILE=/tmp/consul.token",
		}
	} else {
		ppi.SidecarBootEnvVars = []string{
			"SBOOT_READY_FILE=/secrets/ready.val",
			"SBOOT_PROXY_TYPE=" + proxyType,
			"SBOOT_REGISTER_FILE=/secrets/servicereg__" + node.Name + "__" + svc.ID.Name + ".hcl",
			//
			"SBOOT_MODE=direct",
			"SBOOT_TOKEN_FILE=/secrets/service-token--" + svc.ID.ID() + ".val",
		}
	}

	if config.EnterpriseEnabled && node.Partition != "" {
		if !config.EnterpriseDisablePartitions {
			ppi.SidecarBootEnvVars = append(ppi.SidecarBootEnvVars,
				"SBOOT_PARTITION="+node.Partition)
		}
	}

	if config.EncryptionTLSAPI {
		ppi.SidecarBootEnvVars = append(ppi.SidecarBootEnvVars,
			"SBOOT_AGENT_TLS=1")
	}

	return []Resource{
		Eval(tfPingPongAppT, &ppi),
		Eval(tfPingPongSidecarT, &ppi),
	}
}

// TODO: make chaos opt-in
// "-pong-chaos",
var tfPingPongAppT = template.Must(template.ParseFS(content, "templates/container-app.tf.tmpl"))
var tfPingPongSidecarT = template.Must(template.ParseFS(content, "templates/container-app-sidecar.tf.tmpl"))
