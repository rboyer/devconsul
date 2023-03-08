package tfgen

import (
	"sort"
	"strings"
	"text/template"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

type pingpongAppInfo struct {
	PodName    string
	NodeName   string
	PingPong   string // ping or pong
	MetaString string
}

type pingpongSidecarInfo struct {
	pingpongAppInfo
	EnvoyImageResource string
	SidecarBootEnvVars []string
	UseBuiltinProxy    bool
	EnvoyLogLevel      string
}

type pingpongDataplaneInfo struct {
	pingpongAppInfo
	DataplaneImageResource string
	EnvVars                []string
}

func GeneratePingPongContainers(
	config *config.Config,
	topology *infra.Topology,
	podName string,
	node *infra.Node,
) []Resource {
	if node.Service == nil {
		return nil
	}
	svc := node.Service

	switch node.Kind {
	case infra.NodeKindClient, infra.NodeKindDataplane:
	default:
		return nil
	}

	appinfo := pingpongAppInfo{
		PodName:  podName,
		NodeName: node.Name,
		PingPong: svc.ID.Name,
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
		appinfo.MetaString = strings.Join(parts, "--")
		// ppi.MetaString = strings.Join(parts, "--")
	}

	res := make([]Resource, 0, 2)
	res = append(res, Eval(tfPingPongAppT, &appinfo))

	if node.Kind == infra.NodeKindDataplane {
		if node.UseBuiltinProxy {
			panic("not possible")
		}

		dataplaneInfo := pingpongDataplaneInfo{
			pingpongAppInfo:        appinfo,
			DataplaneImageResource: "docker_image.consul-dataplane.latest",
		}

		if node.Canary {
			dataplaneInfo.DataplaneImageResource = "docker_image.consul-dataplane-canary.latest"
		}

		env := make(map[string]string)
		// --- REQUIRED ---
		env["DP_CONSUL_ADDRESSES"] = topology.ServerIPs(node.Cluster)[0]
		// env["DP_CONSUL_ADDRESSES"] = "exec=/bin/echo " +
		// 	strings.Join(topology.ServerIPs(node.Cluster), " ")
		env["DP_SERVICE_NODE_NAME"] = node.PodName() // TODO(cdp): is this enough?
		env["DP_PROXY_SERVICE_ID"] = svc.ID.Name + "-sidecar-proxy"
		// --- enterprise required ---
		if config.EnterpriseEnabled {
			env["DP_SERVICE_NAMESPACE"] = svc.ID.Namespace
			env["DP_SERVICE_PARTITION"] = svc.ID.Partition
		}

		env["DP_LOG_LEVEL"] = config.EnvoyLogLevel

		// envoy
		env["DP_ENVOY_ADMIN_BIND_ADDRESS"] = "0.0.0.0" // for demo purposes
		env["DP_ENVOY_ADMIN_BIND_PORT"] = "19000"

		// acls
		if config.SecurityDisableACLs {
			// nothing
		} else if config.KubernetesEnabled {
			env["DP_CREDENTIAL_TYPE"] = "login"
			env["DP_CREDENTIAL_LOGIN_AUTH_METHOD"] = "minikube"
			env["DP_CREDENTIAL_LOGIN_BEARER_TOKEN_PATH"] = "/secrets/k8s/service_jwt_token." + svc.ID.Name
		} else {
			env["DP_CREDENTIAL_TYPE"] = "static"
			env["SBOOT_TOKEN_FILE"] = "/secrets/service--" + node.Cluster + "--" + svc.ID.ID() + ".val"
			// env["DP_CREDENTIAL_STATIC_TOKEN"] = "<TODO>"
		}

		if config.EncryptionTLSGRPC {
			env["SBOOT_AGENT_GRPC_TLS"] = "1"

			// The path to a file or directory containing CA certificates used to
			// verify the server's certificate. Environment variable: DP_CA_CERTS.
			// env["DP_CA_CERTS"] = "/tls"
			env["DP_CA_CERTS"] = "/tls/consul-agent-ca.pem"
			env["DP_CONSUL_GRPC_PORT"] = "8503"

			env["DP_TLS_SERVER_NAME"] = "server." + node.Cluster + ".consul"
		} else {
			env["DP_TLS_INSECURE_SKIP_VERIFY"] = "1"
			env["DP_CONSUL_GRPC_PORT"] = "8502"
		}

		dataplaneInfo.EnvVars = renderEnv(env)

		res = append(res, Eval(tfPingPongDataplaneT, &dataplaneInfo))
	} else {
		sidecarInfo := pingpongSidecarInfo{
			pingpongAppInfo:    appinfo,
			EnvoyImageResource: "docker_image.consul-envoy.latest",
			UseBuiltinProxy:    node.UseBuiltinProxy,
			EnvoyLogLevel:      config.EnvoyLogLevel,
		}

		if node.Canary {
			sidecarInfo.EnvoyImageResource = "docker_image.consul-envoy-canary.latest"
			// ppi.DataplaneImageResource = "docker_image.consul-dataplane-canary.latest"
		}

		proxyType := "envoy"
		if node.UseBuiltinProxy {
			proxyType = "builtin"
		}

		env := make(map[string]string)
		env["SBOOT_PROXY_TYPE"] = proxyType
		env["SBOOT_REGISTER_FILE"] = "/secrets/servicereg__" + node.Name + "__" + svc.ID.Name + ".hcl"

		if config.SecurityDisableACLs {
			env["SBOOT_MODE"] = "insecure"
		} else if config.KubernetesEnabled {
			env["SBOOT_MODE"] = "login"
			env["SBOOT_BEARER_TOKEN_FILE"] = "/secrets/k8s/service_jwt_token." + svc.ID.Name
			env["SBOOT_TOKEN_SINK_FILE"] = "/tmp/consul.token"
		} else {
			env["SBOOT_MODE"] = "direct"
			env["SBOOT_TOKEN_FILE"] = "/secrets/service--" + node.Cluster + "--" + svc.ID.ID() + ".val"
		}

		if config.EnterpriseEnabled && node.Partition != "" {
			env["SBOOT_PARTITION"] = node.Partition
		}

		if config.EncryptionTLSAPI {
			env["SBOOT_AGENT_TLS"] = "1"
		}
		if config.EncryptionTLSGRPC {
			env["SBOOT_AGENT_GRPC_TLS"] = "1"
		}

		sidecarInfo.SidecarBootEnvVars = renderEnv(env)

		res = append(res, Eval(tfPingPongSidecarT, &sidecarInfo))
	}

	return res
}

func renderEnv(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}

	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// TODO: make chaos opt-in
// "-pong-chaos",
var tfPingPongAppT = template.Must(template.ParseFS(content, "templates/container-app.tf.tmpl"))
var tfPingPongSidecarT = template.Must(template.ParseFS(content, "templates/container-app-sidecar.tf.tmpl"))
var tfPingPongDataplaneT = template.Must(template.ParseFS(content, "templates/container-app-dataplane.tf.tmpl"))
