package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"text/template"

	"github.com/rboyer/safeio"
)

func (t *Tool) commandGen() error {
	var verbose bool

	flag.BoolVar(&verbose, "v", false, "verbose")
	flag.Parse()

	var err error
	t.topology, err = InferTopology(t.config)
	if err != nil {
		return err
	}

	if verbose {
		t.topology.WalkSilent(func(node Node) {
			t.logger.Info("Generating node",
				"name", node.Name,
				"server", node.Server,
				"dc", node.Datacenter,
				"ip", node.IPAddress,
			)
		})
	}

	info := composeInfo{
		Config:        t.config,
		RuntimeConfig: t.runtimeConfig,
	}

	err = t.topology.Walk(func(node Node) error {
		podName := node.Name + "-pod"

		podHCL, err := t.generateAgentHCL(node)
		if err != nil {
			return err
		}

		extraYAML, err := t.generatePingPongYAML(podName, node)
		if err != nil {
			return err
		}

		pod := composePod{
			PodName:        podName,
			ConsulImage:    t.runtimeConfig.ConsulImage,
			Node:           node,
			HCL:            indent(podHCL, 8),
			AgentDependsOn: []string{podName},
			ExtraYAML:      extraYAML,
		}

		if !node.Server {
			pod.AgentDependsOn = append(pod.AgentDependsOn,
				node.Datacenter+"-server1",
			)
		}

		info.Volumes = append(info.Volumes, node.Name)
		info.Pods = append(info.Pods, pod)
		return nil
	})
	if err != nil {
		return err
	}

	var out bytes.Buffer
	if err := dockerComposeT.Execute(&out, &info); err != nil {
		return err
	}

	return t.updateFileIfDifferent(out.Bytes(), "docker-compose.yml", 0644)
}

func (t *Tool) updateFileIfDifferent(body []byte, path string, perm os.FileMode) error {
	prev, err := ioutil.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		t.logger.Info("writing new file", "path", path)
	} else {
		// loaded
		if bytes.Equal(body, prev) {
			return nil
		}
		t.logger.Info("file has changed", "path", path)
	}

	_, err = safeio.WriteToFile(bytes.NewReader(body), path, perm)
	return err
}

type composeInfo struct {
	Config        *Config
	RuntimeConfig RuntimeConfig

	Volumes []string
	Pods    []composePod
}

type composePod struct {
	PodName        string
	ConsulImage    string
	Node           Node
	HCL            string
	AgentDependsOn []string
	ExtraYAML      string
}

var dockerComposeT = template.Must(template.New("docker").Parse(`version: '3.7'

# consul:
#   client_addr is set to 0.0.0.0 to make control from the host easier
#   it should be disabled for real topologies

# envoy:
#   admin-bind is set to 0.0.0.0 to make control from the host easier
#   it should be disabled for real topologies

networks:
  consul:
    ipam:
      driver: default
      config:
        - subnet: '10.0.0.0/16'

volumes:
{{- range .Volumes }}
  {{.}}:
{{- end }}

# https://yipee.io/2017/06/getting-kubernetes-pod-features-using-native-docker-commands/
services:
{{- range .Pods }}
  {{.PodName}}:
    container_name: '{{.PodName}}'
    hostname: '{{.PodName}}'
    image: gcr.io/google_containers/pause:1.0
    restart: always
    dns: 8.8.8.8
    networks:
      consul:
        ipv4_address: '{{.Node.IPAddress}}'

  {{.Node.Name}}:
    network_mode: 'service:{{.PodName}}'
    depends_on:
{{- range .AgentDependsOn }}
      - '{{.}}'
{{- end}}
    volumes:
      - '{{.Node.Name}}:/consul/data'
      - './cache/tls:/tls:ro'
    image: '{{.ConsulImage}}'
    command:
      - 'agent'
      - '-hcl'
      - |
{{ .HCL }}
{{ .ExtraYAML }}
{{- end}}
`))

func (t *Tool) generatePingPongYAML(podName string, node Node) (string, error) {
	var extraYAML bytes.Buffer
	for _, svc := range node.Services {
		switch svc.Name {
		case "ping", "pong":
		default:
			return "", errors.New("unexpected service: " + svc.Name)
		}

		ppi := pingpongInfo{
			PodName:  podName,
			NodeName: node.Name,
			PingPong: svc.Name,
		}

		if t.config.Kubernetes.Enabled {
			ppi.SidecarBootArgs = []string{
				"/secrets/ready.val",
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
				"direct",
				"-t",
				"/secrets/service-token--" + svc.Name + ".val",
				"-r",
				"/secrets/servicereg__" + node.Name + "__" + svc.Name + ".hcl",
			}
		}

		if err := pingpongT.Execute(&extraYAML, &ppi); err != nil {
			return "", err
		}
	}
	return extraYAML.String(), nil
}

type pingpongInfo struct {
	PodName         string
	NodeName        string
	PingPong        string // ping or pong
	SidecarBootArgs []string
}

var pingpongT = template.Must(template.New("pingpong").Parse(`  #####################
  {{.NodeName}}-{{.PingPong}}:
    network_mode: 'service:{{.PodName}}'
    depends_on:
      - {{.NodeName}}
    image: rboyer/pingpong:latest
    init: true
    command:
      - '-bind'
      # - '127.0.0.1:8080'
      - '0.0.0.0:8080'
      - '-dial'
      - '127.0.0.1:9090'

  {{.NodeName}}-{{.PingPong}}-sidecar:
    network_mode: 'service:{{.PodName}}'
    depends_on:
      - {{.NodeName}}-{{.PingPong}}
    image: local/consul-envoy
    init: true
    restart: on-failure
    volumes:
      - './cache:/secrets:ro'
      - './sidecar-boot.sh:/bin/sidecar-boot.sh:ro'
    command:
      - '/bin/sidecar-boot.sh'
{{- range .SidecarBootArgs }}
      - '{{.}}'
{{- end}}
      - '--'
      #################
      - '-sidecar-for'
      - '{{.PingPong}}'
      - '-admin-bind'
      # for demo purposes
      - '0.0.0.0:19000'
      ## debug
      # - '--'
      # - '-l'
      # - 'trace'
`))

func (t *Tool) generateAgentHCL(node Node) (string, error) {
	configInfo := consulAgentConfigInfo{
		RetryJoin:        `"` + strings.Join(t.topology.ServerIPs(node.Datacenter), `", "`) + `"`,
		Datacenter:       node.Datacenter,
		AgentMasterToken: t.runtimeConfig.AgentMasterToken,
		Server:           node.Server,
		GossipKey:        t.runtimeConfig.GossipKey,
		TLS:              t.config.Encryption.TLS,
	}

	if node.Server {
		configInfo.MasterToken = t.config.InitialMasterToken
		leaderDC1 := t.topology.LeaderIP("dc1")
		leaderDC2 := t.topology.LeaderIP("dc2")
		configInfo.RetryJoinWAN = `"` + leaderDC1 + `", "` + leaderDC2 + `"`

		configInfo.SecondaryServer = node.Datacenter != "dc1"
		configInfo.BootstrapExpect = len(t.topology.ServerIPs(node.Datacenter))

		configInfo.TLSFilePrefix = node.Datacenter + "-server-consul-" + strconv.Itoa(node.Index)
	} else {
		configInfo.TLSFilePrefix = node.Datacenter + "-client-consul-" + strconv.Itoa(node.Index)
	}

	var buf bytes.Buffer
	if err := consulAgentConfigT.Execute(&buf, &configInfo); err != nil {
		return "", err
	}

	return buf.String(), nil
}

type consulAgentConfigInfo struct {
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
}

var consulAgentConfigT = template.Must(template.New("consul-agent-config").Parse(`
{{ if .Server -}}
bootstrap_expect       = {{.BootstrapExpect}}
{{- end}}
client_addr            = "0.0.0.0"
datacenter             = "{{.Datacenter}}"
disable_update_check   = true
log_level              = "debug"

primary_datacenter     = "dc1"
retry_join             = [ {{.RetryJoin}} ]
{{ if .Server -}}
retry_join_wan         = [ {{.RetryJoinWAN}} ]
{{- end}}
server                 = {{.Server}}
ui                     = true

{{ if .GossipKey }}
encrypt                = "{{.GossipKey}}"
{{- end }}

{{ if .TLS -}}
ca_file                = "/tls/consul-agent-ca.pem"
cert_file              = "/tls/{{.TLSFilePrefix}}.pem"
key_file               = "/tls/{{.TLSFilePrefix}}-key.pem"
verify_incoming        = true
verify_outgoing        = true
verify_server_hostname = true
{{- end }}

connect {
  enabled = true
}

{{ if not .Server -}}
ports {
  grpc = 8502
}
{{- end }}

acl {
  enabled                  = true
  default_policy           = "deny"
  down_policy              = "extend-cache"
  enable_token_persistence = true
  {{ if .SecondaryServer -}}
  enable_token_replication = true
  {{- end}}
  tokens {
    {{ if .MasterToken -}}
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
