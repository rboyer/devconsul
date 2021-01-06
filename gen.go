package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/rboyer/safeio"
)

func (c *Core) runGenerate(primaryOnly bool) error {
	if err := checkHasRunOnce("init"); err != nil {
		return err
	}

	c.topology.WalkSilent(func(node *Node) {
		c.logger.Info("Generating node",
			"name", node.Name,
			"server", node.Server,
			"dc", node.Datacenter,
			"ip", node.LocalAddress(),
		)
	})

	if err := c.generateConfigs(primaryOnly); err != nil {
		return err
	}

	if c.config.PrometheusEnabled {
		if err := c.generatePrometheusConfigFile(); err != nil {
			return err
		}
		if err := c.generateGrafanaConfigFiles(); err != nil {
			return err
		}
	}

	return c.terraformApply()
}

func (c *Core) generateConfigs(primaryOnly bool) error {
	// write it to a cache file just so we can detect full-destroy
	networks, err := c.writeDockerNetworksTF()
	if err != nil {
		return err
	}

	type terraformPod struct {
		PodName string
		Node    *Node
		HCL     string
		Labels  map[string]string
	}

	var (
		volumes    []string
		images     []string
		containers []string
	)

	addVolume := func(name string) {
		volumes = append(volumes, fmt.Sprintf(`
resource "docker_volume" %[1]q {
  name       = %[1]q
  labels {
    label = "devconsul"
    value = "1"
  }
}`, name))
	}

	addImage := func(name, image string) {
		images = append(images, fmt.Sprintf(`
resource "docker_image" %[1]q {
  name = %[2]q
  keep_locally = true
}`, name, image))
	}

	if c.config.PrometheusEnabled {
		addVolume("prometheus-data")
		addVolume("grafana-data")
	}

	addImage("pause", "k8s.gcr.io/pause:3.3")
	addImage("consul", c.config.ConsulImage)
	addImage("consul-envoy", "local/consul-envoy:latest")
	addImage("pingpong", "rboyer/pingpong:latest")

	err = c.topology.Walk(func(node *Node) error {
		podName := node.Name + "-pod"

		podHCL, err := c.generateAgentHCL(node)
		if err != nil {
			return err
		}

		pod := terraformPod{
			PodName: podName,
			Node:    node,
			HCL:     podHCL,
			Labels:  map[string]string{
				//
			},
		}
		node.AddLabels(pod.Labels)

		// if !node.Server {
		// 	pod.DependsOn = append(pod.DependsOn,
		// 		"docker_container."+node.Datacenter+"-server1",
		// 	)
		// }
		/*
					depends_on = [
			    aws_iam_role_policy.example,
			  ]
		*/

		// NOTE: primaryOnly implies we still generate empty pods in the remote datacenters
		populatePodContents := true
		if primaryOnly {
			populatePodContents = node.Datacenter == PrimaryDC
		}

		addVolume(node.Name)

		// pod placeholder container
		pauseRes, err := stringTemplate(tfPauseT, &pod)
		if err != nil {
			return err
		}
		containers = append(containers, pauseRes)

		if populatePodContents {
			// TODO: container specific consul versions
			// consul agent (TODO: depends on?)
			consulRes, err := stringTemplate(tfConsulT, &pod)
			if err != nil {
				return err
			}
			containers = append(containers, consulRes)

			if gwRes, err := c.generateMeshGatewayContainer(podName, pod.Node); err != nil {
				return err
			} else if gwRes != "" {
				containers = append(containers, gwRes)
			}

			if resources, err := c.generatePingPongContainers(podName, pod.Node); err != nil {
				return err
			} else if len(resources) > 0 {
				containers = append(containers, resources...)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if c.config.PrometheusEnabled {
		addImage("prometheus", "prom/prometheus:latest")
		addImage("grafana", "grafana/grafana:latest")
		containers = append(containers, tfPrometheusContainer)
		containers = append(containers, tfGrafanaContainer)
	}

	var res []string
	res = append(res, networks...)
	res = append(res, volumes...)
	res = append(res, images...)
	res = append(res, containers...)

	_, err = c.writeResourceFile(res, "docker.tf", 0644)
	return err
}

func (c *Core) writeDockerNetworksTF() ([]string, error) {
	var res []string
	for _, net := range c.topology.Networks() {
		res = append(res, fmt.Sprintf(`
resource "docker_network" %[1]q {
  name       = %[1]q
  attachable = true
  ipam_config {
    subnet = %[2]q
  }
  labels {
    label = "devconsul"
    value = "1"
  }
}`, net.DockerName(), net.CIDR))
	}

	updateResult, err := c.writeResourceFile(res, "cache/networks.tf", 0644)
	if err != nil {
		return nil, err
	}

	// You will need to do a full down/up cycle to switch network_shape.
	if updateResult == UpdateResultModified {
		return nil, fmt.Errorf("Networking changed significantly, so you'll have to destroy everything first with 'devconsul down'")
	}
	return res, nil
}

var tfPauseT = template.Must(template.New("tf-pause").Parse(`
resource "docker_container" "{{.PodName}}" {
  name     = "{{.PodName}}"
  image = docker_image.pause.latest
  hostname = "{{.PodName}}"
  restart  = "always"
  dns      = ["8.8.8.8"]

  labels {
    label = "devconsul"
    value = "1"
  }
  labels {
    label = "devconsul.type"
    value = "pod"
  }
{{- range $k, $v := .Labels }}
  labels {
    label = "{{ $k }}"
    value = "{{ $v }}"
  }
{{- end }}

{{- range .Node.Addresses }}
networks_advanced {
  name         = docker_network.devconsul-{{.Network}}.name
  ipv4_address = "{{.IPAddress}}"
}
{{- end }}
}
`))

var tfConsulT = template.Must(template.New("tf-consul").Parse(`
resource "docker_container" "{{.Node.Name}}" {
  name         = "{{.Node.Name}}"
  network_mode = "container:${docker_container.{{.PodName}}.id}"
  image        = docker_image.consul.latest
  restart  = "always"

  labels {
    label = "devconsul"
    value = "1"
  }
  labels {
    label = "devconsul.type"
    value = "consul"
  }
{{- range $k, $v := .Labels }}
  labels {
    label = "{{ $k }}"
    value = "{{ $v }}"
  }
{{- end }}

  command = [
    "agent",
    "-hcl",
	<<-EOT
{{ .HCL }}
EOT
  ]

  volumes {
    volume_name    = "{{.Node.Name}}"
    container_path = "/consul/data"
  }
  volumes {
    host_path      = abspath("cache/tls")
    container_path = "/tls"
    read_only      = true
  }
}
`))

func (c *Core) generateMeshGatewayContainer(podName string, node *Node) (string, error) {
	if !node.MeshGateway {
		return "", nil
	}

	type tfMeshGatewayInfo struct {
		PodName       string
		NodeName      string
		EnvoyLogLevel string
		EnableWAN     bool
		LANAddress    string
		WANAddress    string
		ExposeServers bool
		Labels        map[string]string
	}

	mgi := tfMeshGatewayInfo{
		PodName:       podName,
		NodeName:      node.Name,
		EnvoyLogLevel: c.config.EnvoyLogLevel,
		Labels:        map[string]string{
			//
		},
	}
	node.AddLabels(mgi.Labels)

	switch c.topology.NetworkShape {
	case NetworkShapeIslands, NetworkShapeDual:
		mgi.EnableWAN = true
		mgi.ExposeServers = true
		mgi.LANAddress = `{{ GetInterfaceIP \"eth0\" }}:8443`
		mgi.WANAddress = `{{ GetInterfaceIP \"eth1\" }}:8443`
	case NetworkShapeFlat:
	default:
		panic("unknown shape: " + c.topology.NetworkShape)
	}

	return stringTemplate(tfMeshGatewayT, &mgi)
}

var tfMeshGatewayT = template.Must(template.New("tf-mesh-gateway").Parse(`
resource "docker_container" "{{.NodeName}}-mesh-gateway" {
	name = "{{.NodeName}}-mesh-gateway"
    network_mode = "container:${docker_container.{{.PodName}}.id}"
	image        = docker_image.consul-envoy.latest
    restart  = "on-failure"

  labels {
    label = "devconsul"
    value = "1"
  }
  labels {
    label = "devconsul.type"
    value = "gateway"
  }
{{- range $k, $v := .Labels }}
  labels {
    label = "{{ $k }}"
    value = "{{ $v }}"
  }
{{- end }}

  volumes {
    host_path      = abspath("cache")
    container_path = "/secrets"
    read_only      = true
  }
  volumes {
    host_path      = abspath("mesh-gateway-sidecar-boot.sh")
    container_path = "/bin/mesh-gateway-sidecar-boot.sh"
    read_only      = true
  }

  command = [
      "/bin/mesh-gateway-sidecar-boot.sh",
      "/secrets/ready.val",
      "-t",
      "/secrets/mesh-gateway.val",
      "--",
{{- if .ExposeServers }}
      "-expose-servers",
{{- end }}
{{- if .EnableWAN }}
      "-address",
      "{{ .LANAddress }}",
      "-wan-address",
      "{{ .WANAddress }}",
{{- end }}
      "-admin-bind",
      // for demo purposes
      "0.0.0.0:19000",
      "--",
      "-l",
      "{{ .EnvoyLogLevel }}",
  ]
}
`))

func (c *Core) generatePingPongContainers(podName string, node *Node) ([]string, error) {
	if node.Service == nil {
		return nil, nil
	}
	svc := node.Service

	switch svc.Name {
	case "ping", "pong":
	default:
		return nil, errors.New("unexpected service: " + svc.Name)
	}

	type pingpongInfo struct {
		PodName         string
		NodeName        string
		PingPong        string // ping or pong
		MetaString      string
		SidecarBootArgs []string
		UseBuiltinProxy bool
		EnvoyLogLevel   string
	}

	ppi := pingpongInfo{
		PodName:         podName,
		NodeName:        node.Name,
		PingPong:        svc.Name,
		UseBuiltinProxy: node.UseBuiltinProxy,
		EnvoyLogLevel:   c.config.EnvoyLogLevel,
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

	if c.config.KubernetesEnabled {
		ppi.SidecarBootArgs = []string{
			"/secrets/ready.val",
			proxyType,
			"login",
			"-t",
			"/secrets/k8s/service_jwt_token." + svc.Name,
			"-s",
			"/tmp/consul.token",
			"-r",
			"/secrets/servicereg__" + node.Name + "__" + svc.Name + ".hcl",
		}
	} else {
		ppi.SidecarBootArgs = []string{
			"/secrets/ready.val",
			proxyType,
			"direct",
			"-t",
			"/secrets/service-token--" + svc.Name + ".val",
			"-r",
			"/secrets/servicereg__" + node.Name + "__" + svc.Name + ".hcl",
		}
	}

	appRes, err := stringTemplate(tfPingPongAppT, &ppi)
	if err != nil {
		return nil, err
	}
	sidecarRes, err := stringTemplate(tfPingPongSidecarT, &ppi)
	if err != nil {
		return nil, err
	}

	return []string{appRes, sidecarRes}, nil
}

// TODO: make chaos opt-in
var tfPingPongAppT = template.Must(template.New("tf-pingpong-app").Parse(`
resource "docker_container" "{{.NodeName}}-{{.PingPong}}" {
	name = "{{.NodeName}}-{{.PingPong}}"
    network_mode = "container:${docker_container.{{.PodName}}.id}"
	image        = docker_image.pingpong.latest
    restart  = "on-failure"

  labels {
    label = "devconsul"
    value = "1"
  }
  labels {
    label = "devconsul.type"
    value = "app"
  }

  command = [
      "-bind",
      "0.0.0.0:8080",
      "-dial",
      "127.0.0.1:9090",
      "-pong-chaos",
      "-dialfreq",
      "5ms",
      "-name",
      "{{.PingPong}}{{.MetaString}}",
  ]
}`))

var tfPingPongSidecarT = template.Must(template.New("tf-pingpong-sidecar").Parse(`
resource "docker_container" "{{.NodeName}}-{{.PingPong}}-sidecar" {
	name = "{{.NodeName}}-{{.PingPong}}-sidecar"
    network_mode = "container:${docker_container.{{.PodName}}.id}"
	image        = docker_image.consul-envoy.latest
    restart  = "on-failure"

  labels {
    label = "devconsul"
    value = "1"
  }
  labels {
    label = "devconsul.type"
    value = "sidecar"
  }

  volumes {
    host_path      = abspath("cache")
    container_path = "/secrets"
    read_only      = true
  }
  volumes {
    host_path      = abspath("sidecar-boot.sh")
    container_path = "/bin/sidecar-boot.sh"
    read_only      = true
  }

  command = [
      "/bin/sidecar-boot.sh",
{{- range .SidecarBootArgs }}
      "{{.}}",
{{- end}}
      "--",
      #################
      "-sidecar-for",
      "{{.PingPong}}",
{{- if not .UseBuiltinProxy }}
      "-admin-bind",
      # for demo purposes
      "0.0.0.0:19000",
      "--",
      "-l",
      "{{ .EnvoyLogLevel }}",
{{- end }}
  ]
}
`))

const tfPrometheusContainer = `
resource "docker_container" "prometheus" {
  name  = "prometheus"
  image = docker_image.prometheus.latest
  labels {
    label = "devconsul"
    value = "1"
  }
  labels {
    label = "devconsul.type"
    value = "infra"
  }
  restart = "always"
  dns     = ["8.8.8.8"]
  volumes {
    volume_name    = "prometheus-data"
    container_path = "/prometheus-data"
  }
  volumes {
    host_path      = abspath("cache/prometheus.yml")
    container_path = "/etc/prometheus/prometheus.yml"
    read_only      = true
   }
  networks_advanced {
    name         = docker_network.devconsul-lan.name
    ipv4_address = "10.0.100.100"
   }

  ports {
    internal = 9090
    external = 9090
   }
  ports {
    internal = 3000
    external = 3000
  }
} `

const tfGrafanaContainer = `
resource "docker_container" "grafana" {
  name  = "grafana"
  image = docker_image.grafana.latest
  labels {
    label = "devconsul"
    value = "1"
  }
  labels {
    label = "devconsul.type"
    value = "infra"
  }
  restart = "always"
  network_mode = "container:${docker_container.prometheus.id}"
  volumes {
    volume_name    = "grafana-data"
    container_path = "/var/lib/grafana"
  }
  volumes {
    host_path      = abspath("cache/grafana-prometheus.yml")
    container_path = "/etc/grafana/provisioning/datasources/prometheus.yml"
    read_only      = true
  }
  volumes {
    host_path      = abspath("cache/grafana.ini")
    container_path = "/etc/grafana/grafana.ini"
    read_only      = true
  }
} `

func (c *Core) generateAgentHCL(node *Node) (string, error) {
	type consulAgentConfigInfo struct {
		AdvertiseAddr    string
		AdvertiseAddrWAN string
		RetryJoin        string
		RetryJoinWAN     string
		Datacenter       string
		SecondaryServer  bool
		MasterToken      string
		AgentMasterToken string
		Server           bool
		BootstrapExpect  int
		GossipKey        string
		TLS              bool
		TLSFilePrefix    string
		Prometheus       bool

		FederateViaGateway  bool
		PrimaryGateways     string
		DisableWANBootstrap bool
	}

	configInfo := consulAgentConfigInfo{
		AdvertiseAddr:    node.LocalAddress(),
		RetryJoin:        `"` + strings.Join(c.topology.ServerIPs(node.Datacenter), `", "`) + `"`,
		Datacenter:       node.Datacenter,
		AgentMasterToken: c.config.AgentMasterToken,
		Server:           node.Server,
		GossipKey:        c.config.GossipKey,
		TLS:              c.config.EncryptionTLS,
		Prometheus:       c.config.PrometheusEnabled,
	}

	if node.Server {
		configInfo.MasterToken = c.config.InitialMasterToken

		wanIP := false
		wanfed := false
		switch c.topology.NetworkShape {
		case NetworkShapeIslands:
			wanfed = true
			if node.MeshGateway {
				wanIP = true
				configInfo.AdvertiseAddrWAN = node.PublicAddress()
			}
		case NetworkShapeDual:
			wanIP = true
			configInfo.AdvertiseAddrWAN = node.PublicAddress()
		case NetworkShapeFlat:
			// n/a
		default:
			panic("unknown shape: " + c.topology.NetworkShape)
		}

		var ips []string
		for _, dc := range c.topology.Datacenters() {
			ips = append(ips, c.topology.LeaderIP(dc.Name, wanIP))
		}

		if wanfed {
			configInfo.FederateViaGateway = true
			if node.Datacenter != PrimaryDC {
				primaryGateways := c.topology.GatewayAddrs(PrimaryDC)
				configInfo.PrimaryGateways = `"` + strings.Join(primaryGateways, `", "`) + `"`
				configInfo.DisableWANBootstrap = c.topology.DisableWANBootstrap
			}
		} else {
			configInfo.RetryJoinWAN = `"` + strings.Join(ips, `", "`) + `"`
		}

		configInfo.SecondaryServer = node.Datacenter != PrimaryDC
		configInfo.BootstrapExpect = len(c.topology.ServerIPs(node.Datacenter))

		configInfo.TLSFilePrefix = node.Datacenter + "-server-consul-" + strconv.Itoa(node.Index)
	} else {
		configInfo.TLSFilePrefix = node.Datacenter + "-client-consul-" + strconv.Itoa(node.Index)
	}

	var buf bytes.Buffer
	if err := consulAgentConfigT.Execute(&buf, &configInfo); err != nil {
		return "", err
	}

	// Ensure it looks tidy
	out := hclwrite.Format(buf.Bytes())
	return string(out), nil
}

var consulAgentConfigT = template.Must(template.New("consul-agent-config").Parse(`
{{ if .Server -}}
bootstrap_expect       = {{.BootstrapExpect}}
{{- end}}
client_addr            = "0.0.0.0"
advertise_addr         = "{{.AdvertiseAddr }}"
{{ if .AdvertiseAddrWAN -}}
advertise_addr_wan     = "{{.AdvertiseAddrWAN }}"
{{- end}}
translate_wan_addrs    = true
client_addr            = "0.0.0.0"
datacenter             = "{{.Datacenter}}"
disable_update_check   = true
log_level              = "trace"

enable_debug                  = true

use_streaming_backend = true

{{ if .Server }}
rpc {
  enable_streaming = true
}
{{ end }}

primary_datacenter     = "dc1"
retry_join             = [ {{.RetryJoin}} ]
{{ if .FederateViaGateway -}}
{{ if .SecondaryServer -}}
primary_gateways          = [ {{ .PrimaryGateways }} ]
primary_gateways_interval = "5s"
{{ if .DisableWANBootstrap -}}
disable_primary_gateway_fallback = true
{{- end}}
{{- end}}
{{ else -}}
{{ if .Server -}}
retry_join_wan         = [ {{.RetryJoinWAN}} ]
{{- end}}
{{- end}}
server                 = {{.Server}}

ui_config {
  enabled          = true
{{ if .Prometheus }}
  metrics_provider = "prometheus"
  metrics_proxy {
	base_url = "http://prometheus:9090"
  }
{{ end }}
}

{{ if .Prometheus }}
telemetry {
  prometheus_retention_time = "168h"
}
{{ end }}

{{ if .GossipKey }}
encrypt                = "{{.GossipKey}}"
{{- end }}

{{ if .TLS }}
ca_file                = "/tls/consul-agent-ca.pem"
cert_file              = "/tls/{{.TLSFilePrefix}}.pem"
key_file               = "/tls/{{.TLSFilePrefix}}-key.pem"
verify_incoming        = true
verify_outgoing        = true
verify_server_hostname = true
{{ end }}

{{ if not .SecondaryServer }}
# Exercise config entry bootstrap
config_entries {
  bootstrap {
    kind     = "service-defaults"
    name     = "placeholder"
    protocol = "grpc"
  }

  bootstrap {
    kind = "service-intentions"
    name = "placeholder"
    sources {
      name   = "placeholder-client"
      action = "allow"
    }
  }
}
{{ end}}

connect {
  enabled = true
  {{ if .FederateViaGateway -}}
  enable_mesh_gateway_wan_federation = true
  {{- end}}
}

{{ if not .Server }}
ports {
  grpc = 8502
}
{{ end }}

acl {
  enabled                  = true
  default_policy           = "deny"
  down_policy              = "extend-cache"
  enable_token_persistence = true
  {{ if .SecondaryServer -}}
  enable_token_replication = true
  {{- end}}
  tokens {
    {{ if and .MasterToken .Server (not .SecondaryServer) -}}
    master       = "{{.MasterToken}}"
    {{- end }}
    agent_master = "{{.AgentMasterToken}}"
  }
}
`))

func indent(s string, n int) string {
	prefix := strings.Repeat(" ", n)

	var buf bytes.Buffer

	scan := bufio.NewScanner(strings.NewReader(s))
	for scan.Scan() {
		line := scan.Text()
		if strings.TrimSpace(scan.Text()) != "" {
			buf.WriteString(prefix + line + "\n")
		}
	}
	if scan.Err() != nil {
		panic("impossible to indent: " + scan.Err().Error())
	}

	return buf.String()
}

func (c *Core) generatePrometheusConfigFile() error {
	type kv struct {
		Key, Val string
	}
	type job struct {
		Name        string
		MetricsPath string
		Params      map[string][]string
		Targets     []string
		Labels      []kv
	}

	jobs := make(map[string]*job)
	add := func(j *job) {
		prev, ok := jobs[j.Name]
		if ok {
			// only retain targets
			prev.Targets = append(prev.Targets, j.Targets...)
			j = prev
		} else {
			sort.Slice(j.Labels, func(a, b int) bool {
				return j.Labels[a].Key < j.Labels[b].Key
			})
			jobs[j.Name] = j
		}
		sort.Strings(j.Targets)
	}

	err := c.topology.Walk(func(node *Node) error {
		if node.Server {
			add(&job{
				Name:        "consul-servers-" + node.Datacenter,
				MetricsPath: "/v1/agent/metrics",
				Params: map[string][]string{
					"format": []string{"prometheus"},
					"token":  []string{c.config.AgentMasterToken},
				},
				Targets: []string{
					net.JoinHostPort(node.LocalAddress(), "8500"),
				},
				Labels: []kv{
					{"dc", node.Datacenter},
					// {"node", node.Name},
					{"role", "consul-server"},
				},
			})
		} else {
			add(&job{
				Name:        "consul-clients-" + node.Datacenter,
				MetricsPath: "/v1/agent/metrics",
				Params: map[string][]string{
					"format": []string{"prometheus"},
					"token":  []string{c.config.AgentMasterToken},
				},
				Targets: []string{
					net.JoinHostPort(node.LocalAddress(), "8500"),
				},
				Labels: []kv{
					{"dc", node.Datacenter},
					// {"node", node.Name},
					{"role", "consul-client"},
				},
			})

			if node.MeshGateway {
				add(&job{
					Name:        "mesh-gateways-" + node.Datacenter,
					MetricsPath: "/metrics",
					Targets: []string{
						net.JoinHostPort(node.LocalAddress(), "9102"),
					},
					Labels: []kv{
						{"dc", node.Datacenter},
						// {"node", node.Name},
						{"role", "mesh-gateway"},
					},
				})
			} else if node.Service != nil {
				add(&job{
					Name:        node.Service.Name + "-proxy",
					MetricsPath: "/metrics",
					Targets: []string{
						net.JoinHostPort(node.LocalAddress(), "9102"),
					},
					Labels: []kv{
						{"dc", node.Datacenter},
						// {"node", node.Name},
						{"role", node.Service.Name + "-proxy"},
					},
				})
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	info := struct {
		Jobs []*job
	}{}
	for _, j := range jobs {
		info.Jobs = append(info.Jobs, j)
	}
	sort.Slice(info.Jobs, func(i, j int) bool {
		return info.Jobs[i].Name < info.Jobs[j].Name
	})

	var out bytes.Buffer
	if err := prometheusConfigT.Execute(&out, &info); err != nil {
		return err
	}

	_, err = c.updateFileIfDifferent(out.Bytes(), "cache/prometheus.yml", 0644)
	return err
}

var prometheusConfigT = template.Must(template.New("prometheus").Parse(`
# my global config
global:
  scrape_interval:     5s
  evaluation_interval: 5s

# Alertmanager configuration
alerting:
  alertmanagers:
  - static_configs:
    - targets:
      # - alertmanager:9093

# Load rules once and periodically evaluate them according to the global 'evaluation_interval'.
rule_files:
  # - "first_rules.yml"
  # - "second_rules.yml"

# A scrape configuration containing exactly one endpoint to scrape:
# Here it's Prometheus itself.
scrape_configs:
  - job_name: 'prometheus'

    # metrics_path defaults to '/metrics'
    # scheme defaults to 'http'.

    static_configs:
    - targets: ['localhost:9090']

{{- range .Jobs }}

  - job_name: {{.Name}}
    metrics_path: "{{.MetricsPath}}"
    params:
{{- range $k, $vl := .Params }}
      {{ $k }}:
{{- range $vl }}
      - {{ . }}
{{- end}}
{{- end}}
    static_configs:
    - targets:
{{- range .Targets }}
      - "{{ . }}"
{{- end }}
      labels:
{{- range .Labels }}
        {{ .Key }}: "{{ .Val }}"
{{- end }}
{{- end }}
`))

func (c *Core) generateGrafanaConfigFiles() error {
	files := map[string]string{
		"grafana-prometheus.yml": `
apiVersion: 1

datasources:
- name: Prometheus
  type: prometheus
  access: proxy
  url: http://localhost:9090
  isDefault: true
  version: 1
  editable: false
`,
		"grafana.ini": `
[auth.anonymous]
enabled = true

# Organization name that should be used for unauthenticated users
org_name = Main Org.

# Role for unauthenticated users, other valid values are 'Editor' and 'Admin'
org_role = Admin
`,
	}

	for name, body := range files {
		if _, err := c.updateFileIfDifferent([]byte(body), filepath.Join("cache", name), 0644); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) writeResourceFile(res []string, path string, perm os.FileMode) (UpdateResult, error) {
	for i := 0; i < len(res); i++ {
		res[i] = strings.TrimSpace(res[i])
	}

	body := strings.Join(res, "\n\n")

	// Ensure it looks tidy
	out := hclwrite.Format(bytes.TrimSpace([]byte(body)))

	return c.updateFileIfDifferent(out, path, perm)
}

type UpdateResult int

const (
	UpdateResultNone UpdateResult = iota
	UpdateResultCreated
	UpdateResultModified
)

func (c *Core) updateFileIfDifferent(body []byte, path string, perm os.FileMode) (UpdateResult, error) {
	prev, err := ioutil.ReadFile(path)

	result := UpdateResultNone
	if err != nil {
		if !os.IsNotExist(err) {
			return result, err
		}
		c.logger.Info("writing new file", "path", path)
		result = UpdateResultCreated
	} else {
		// loaded
		if bytes.Equal(body, prev) {
			return result, nil
		}
		c.logger.Info("file has changed", "path", path)
		result = UpdateResultModified
	}

	_, err = safeio.WriteToFile(bytes.NewReader(body), path, perm)
	return result, err
}

func stringTemplate(t *template.Template, data interface{}) (string, error) {
	var res bytes.Buffer
	if err := t.Execute(&res, data); err != nil {
		return "", err
	}
	return res.String(), nil
}
