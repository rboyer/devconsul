package main

import (
	"bufio"
	"bytes"
	"embed"
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

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

//go:embed templates/container-pause.tf.tmpl
//go:embed templates/container-consul.tf.tmpl
//go:embed templates/container-mgw.tf.tmpl
//go:embed templates/container-app.tf.tmpl
//go:embed templates/container-app-sidecar.tf.tmpl
//go:embed templates/consul-agent-config.hcl.tmpl
//go:embed templates/prometheus-config.yml.tmpl
//go:embed templates/container-prometheus.tf
//go:embed templates/container-grafana.tf
var content embed.FS

func (c *Core) runGenerate(primaryOnly bool) error {
	if err := checkHasRunOnce("init"); err != nil {
		return err
	}

	c.topology.WalkSilent(func(node *infra.Node) {
		c.logger.Info("Generating node",
			"name", node.Name,
			"server", node.Server,
			"dc", node.Cluster,
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
		PodName               string
		Node                  *infra.Node
		HCL                   string
		Labels                map[string]string
		EnterpriseLicensePath string
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

	if c.config.CanaryEnvoyVersion != "" {
		addImage("consul-envoy-canary", "local/consul-envoy-canary:latest")
	}

	err = c.topology.Walk(func(node *infra.Node) error {
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
			EnterpriseLicensePath: c.config.EnterpriseLicensePath,
		}
		node.AddLabels(pod.Labels)

		// NOTE: primaryOnly implies we still generate empty pods in the remote datacenters
		populatePodContents := true
		if primaryOnly {
			populatePodContents = node.Cluster == config.PrimaryCluster
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

		promBytes, err := content.ReadFile("templates/container-prometheus.tf")
		if err != nil {
			return err
		}
		grafanaBytes, err := content.ReadFile("templates/container-grafana.tf")
		if err != nil {
			return err
		}

		containers = append(containers, string(promBytes))
		containers = append(containers, string(grafanaBytes))
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

var tfPauseT = template.Must(template.ParseFS(content, "templates/container-pause.tf.tmpl"))
var tfConsulT = template.Must(template.ParseFS(content, "templates/container-consul.tf.tmpl"))

func (c *Core) generateMeshGatewayContainer(podName string, node *infra.Node) (string, error) {
	if !node.MeshGateway {
		return "", nil
	}

	type tfMeshGatewayInfo struct {
		PodName            string
		NodeName           string
		EnvoyLogLevel      string
		EnableACLs         bool
		EnableWAN          bool
		LANAddress         string
		WANAddress         string
		ExposeServers      bool
		SidecarBootEnvVars []string
		Labels             map[string]string
	}

	mgi := tfMeshGatewayInfo{
		PodName:       podName,
		NodeName:      node.Name,
		EnvoyLogLevel: c.config.EnvoyLogLevel,
		EnableACLs:    !c.config.SecurityDisableACLs,
		Labels:        map[string]string{
			//
		},
		SidecarBootEnvVars: []string{
			"SBOOT_READY_FILE=/secrets/ready.val",
		},
	}
	node.AddLabels(mgi.Labels)

	if c.config.SecurityDisableACLs {
		mgi.SidecarBootEnvVars = append(mgi.SidecarBootEnvVars,
			"SBOOT_MODE=insecure",
		)
	} else {
		mgi.SidecarBootEnvVars = append(mgi.SidecarBootEnvVars,
			"SBOOT_MODE=direct",
			"SBOOT_TOKEN_FILE=/secrets/mesh-gateway--"+node.Cluster+".val",
		)
	}

	if c.config.EnterpriseEnabled && node.Partition != "" {
		if !c.config.EnterpriseDisablePartitions {
			mgi.SidecarBootEnvVars = append(mgi.SidecarBootEnvVars,
				"SBOOT_PARTITION="+node.Partition)
		}
	}

	if c.config.EncryptionTLSAPI {
		mgi.SidecarBootEnvVars = append(mgi.SidecarBootEnvVars,
			"SBOOT_AGENT_TLS=1")
	}

	switch c.topology.NetworkShape {
	case NetworkShapeIslands, NetworkShapeDual:
		mgi.EnableWAN = true
		mgi.ExposeServers = true
		mgi.LANAddress = `{{ GetInterfaceIP \"eth0\" }}:8443`
		mgi.WANAddress = `{{ GetInterfaceIP \"eth1\" }}:8443`
	case NetworkShapeFlat:
		mgi.EnableWAN = true
		mgi.LANAddress = `{{ GetInterfaceIP \"eth0\" }}:8443`
		if node.MeshGatewayUseDNSWANAddress {
			mgi.WANAddress = node.Name + "-pod:8443"
		} else {
			mgi.WANAddress = `{{ GetInterfaceIP \"eth0\" }}:8443`
		}
	default:
		panic("unknown shape: " + c.topology.NetworkShape)
	}

	return stringTemplate(tfMeshGatewayT, &mgi)
}

var tfMeshGatewayT = template.Must(template.ParseFS(content, "templates/container-mgw.tf.tmpl"))

func (c *Core) generatePingPongContainers(podName string, node *infra.Node) ([]string, error) {
	if node.Service == nil {
		return nil, nil
	}
	svc := node.Service

	switch svc.ID.Name {
	case "ping", "pong":
	default:
		return nil, errors.New("unexpected service: " + svc.ID.Name)
	}

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

	ppi := pingpongInfo{
		PodName:            podName,
		NodeName:           node.Name,
		PingPong:           svc.ID.Name,
		UseBuiltinProxy:    node.UseBuiltinProxy,
		EnvoyLogLevel:      c.config.EnvoyLogLevel,
		EnvoyImageResource: "docker_image.consul-envoy.latest",
		EnableACLs:         !c.config.SecurityDisableACLs,
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

	if c.config.SecurityDisableACLs {
		ppi.SidecarBootEnvVars = []string{
			"SBOOT_READY_FILE=/secrets/ready.val",
			"SBOOT_PROXY_TYPE=" + proxyType,
			"SBOOT_REGISTER_FILE=/secrets/servicereg__" + node.Name + "__" + svc.ID.Name + ".hcl",
			//
			"SBOOT_MODE=insecure",
		}
	} else if c.config.KubernetesEnabled {
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

	if c.config.EnterpriseEnabled && node.Partition != "" {
		if !c.config.EnterpriseDisablePartitions {
			ppi.SidecarBootEnvVars = append(ppi.SidecarBootEnvVars,
				"SBOOT_PARTITION="+node.Partition)
		}
	}

	if c.config.EncryptionTLSAPI {
		ppi.SidecarBootEnvVars = append(ppi.SidecarBootEnvVars,
			"SBOOT_AGENT_TLS=1")
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
// "-pong-chaos",
var tfPingPongAppT = template.Must(template.ParseFS(content, "templates/container-app.tf.tmpl"))
var tfPingPongSidecarT = template.Must(template.ParseFS(content, "templates/container-app-sidecar.tf.tmpl"))

func (c *Core) generateAgentHCL(node *infra.Node) (string, error) {
	type networkSegment struct {
		Name string
		Port int
	}

	type consulAgentConfigInfo struct {
		AdvertiseAddr     string
		AdvertiseAddrWAN  string
		RetryJoin         string
		RetryJoinWAN      string
		Cluster           string
		PrimaryDatacenter string
		SecondaryServer   bool
		MasterToken       string
		AgentMasterToken  string
		Server            bool
		BootstrapExpect   int
		GossipKey         string
		TLS               bool
		TLSAPI            bool
		TLSFilePrefix     string
		EnableACLs        bool
		Prometheus        bool

		FederateViaGateway          bool
		PrimaryGateways             string
		EnterpriseLicensePath       string
		EnterpriseSegment           string
		EnterpriseSegmentPort       int
		EnterpriseSegments          []networkSegment
		EnterprisePartition         string
		EnterpriseDisablePartitions bool
	}

	configInfo := consulAgentConfigInfo{
		AdvertiseAddr:               node.LocalAddress(),
		RetryJoin:                   `"` + strings.Join(c.topology.ServerIPs(node.Cluster), `", "`) + `"`,
		Cluster:                     node.Cluster,
		Server:                      node.Server,
		GossipKey:                   c.config.GossipKey,
		TLS:                         c.config.EncryptionTLS,
		TLSAPI:                      c.config.EncryptionTLSAPI,
		EnableACLs:                  !c.config.SecurityDisableACLs,
		Prometheus:                  c.config.PrometheusEnabled,
		EnterpriseLicensePath:       c.config.EnterpriseLicensePath,
		EnterpriseSegment:           node.Segment,
		EnterpriseSegmentPort:       c.config.EnterpriseSegments[node.Segment],
		EnterpriseDisablePartitions: c.config.EnterpriseDisablePartitions,
		EnterprisePartition:         node.Partition,
	}

	if !c.config.SecurityDisableACLs {
		configInfo.AgentMasterToken = c.config.AgentMasterToken
	}

	switch c.topology.LinkMode {
	case infra.ClusterLinkModeFederate:
		configInfo.PrimaryDatacenter = config.PrimaryCluster
	case infra.ClusterLinkModePeer:
		configInfo.PrimaryDatacenter = node.Cluster
	}

	if c.config.EnterpriseDisablePartitions || !c.config.EnterpriseEnabled {
		configInfo.EnterprisePartition = ""
	}

	if node.Server {
		if !c.config.SecurityDisableACLs {
			configInfo.MasterToken = c.config.InitialMasterToken
		}

		configInfo.EnterpriseSegment = ""    // we dont' configure this setting on servers
		configInfo.EnterpriseSegmentPort = 0 // we dont' configure this setting on servers
		configInfo.EnterprisePartition = ""  // we dont' configure this setting on servers

		for name, port := range c.config.EnterpriseSegments {
			configInfo.EnterpriseSegments = append(configInfo.EnterpriseSegments, networkSegment{Name: name, Port: port})
		}
		sort.Slice(configInfo.EnterpriseSegments, func(i, j int) bool {
			a := configInfo.EnterpriseSegments[i]
			b := configInfo.EnterpriseSegments[j]
			return a.Port < b.Port
		})

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

		if c.topology.LinkWithFederation() {
			if wanfed {
				configInfo.FederateViaGateway = true
				if node.Cluster != config.PrimaryCluster {
					primaryGateways := c.topology.GatewayAddrs(config.PrimaryCluster)
					configInfo.PrimaryGateways = `"` + strings.Join(primaryGateways, `", "`) + `"`
				}
			} else {
				var ips []string
				for _, cluster := range c.topology.Clusters() {
					ips = append(ips, c.topology.LeaderIP(cluster.Name, wanIP))
				}
				configInfo.RetryJoinWAN = `"` + strings.Join(ips, `", "`) + `"`
			}

			configInfo.SecondaryServer = node.Cluster != config.PrimaryCluster
		}
		configInfo.BootstrapExpect = len(c.topology.ServerIPs(node.Cluster))

		configInfo.TLSFilePrefix = node.Cluster + "-server-consul-" + strconv.Itoa(node.Index)
	} else {
		configInfo.TLSFilePrefix = node.Cluster + "-client-consul-" + strconv.Itoa(node.Index)
	}

	var buf bytes.Buffer
	if err := consulAgentConfigT.Execute(&buf, &configInfo); err != nil {
		return "", err
	}

	// Ensure it looks tidy
	out := hclwrite.Format(buf.Bytes())
	return string(out), nil
}

var consulAgentConfigT = template.Must(template.ParseFS(content, "templates/consul-agent-config.hcl.tmpl"))

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
	if c.config.SecurityDisableACLs {
		return fmt.Errorf("prometheus setup is incompatible with insecure consul")
	}
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

	err := c.topology.Walk(func(node *infra.Node) error {
		if node.Server {
			add(&job{
				Name:        "consul-servers-" + node.Cluster,
				MetricsPath: "/v1/agent/metrics",
				Params: map[string][]string{
					"format": {"prometheus"},
					"token":  {c.config.AgentMasterToken},
				},
				Targets: []string{
					net.JoinHostPort(node.LocalAddress(), "8500"),
				},
				Labels: []kv{
					{"cluster", node.Cluster},
					{"partition", "default"},
					{"node", node.Name},
					{"role", "consul-server"},
				},
			})
		} else {
			add(&job{
				Name:        "consul-clients-" + node.Cluster,
				MetricsPath: "/v1/agent/metrics",
				Params: map[string][]string{
					"format": {"prometheus"},
					"token":  {c.config.AgentMasterToken},
				},
				Targets: []string{
					net.JoinHostPort(node.LocalAddress(), "8500"),
				},
				Labels: []kv{
					{"cluster", node.Cluster},
					{"partition", node.Partition},
					{"segment", node.Segment},
					{"node", node.Name},
					{"role", "consul-client"},
				},
			})

			if node.MeshGateway {
				add(&job{
					Name:        "mesh-gateways-" + node.Cluster,
					MetricsPath: "/metrics",
					Targets: []string{
						net.JoinHostPort(node.LocalAddress(), "9102"),
					},
					Labels: []kv{
						{"cluster", node.Cluster},
						{"namespace", "default"},
						{"partition", node.Partition},
						{"segment", node.Segment},
						{"node", node.Name},
						{"role", "mesh-gateway"},
					},
				})
			} else if node.Service != nil {
				add(&job{
					Name:        node.Service.ID.Name + "-proxy",
					MetricsPath: "/metrics",
					Targets: []string{
						net.JoinHostPort(node.LocalAddress(), "9102"),
					},
					Labels: []kv{
						{"cluster", node.Cluster},
						{"namespace", node.Service.ID.Namespace},
						{"partition", node.Service.ID.Partition},
						// {"node", node.Name},
						{"role", node.Service.ID.Name + "-proxy"},
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

var prometheusConfigT = template.Must(template.ParseFS(content, "templates/prometheus-config.yml.tmpl"))

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
