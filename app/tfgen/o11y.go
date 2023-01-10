package tfgen

import (
	"fmt"
	"net"
	"sort"
	"text/template"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

func GrafanaINI() *FileResource {
	return File("cache/grafana.ini",
		Embed("templates/grafana.ini"))
}

func GrafanaPrometheus() *FileResource {
	return File("cache/grafana-prometheus.yml",
		Embed("templates/grafana-prometheus.yml"))
}

func PrometheusContainer() Resource {
	return Embed("templates/container-prometheus.tf")
}

func GrafanaContainer() Resource {
	return Embed("templates/container-grafana.tf")
}

func GeneratePrometheusConfigFile(cfg *config.Config, topology *infra.Topology) *FileResource {
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
		if _, ok := jobs[j.Name]; ok {
			panic(fmt.Errorf("duplicate detected: %q", j.Name))
		}

		sort.Slice(j.Labels, func(a, b int) bool {
			return j.Labels[a].Key < j.Labels[b].Key
		})
		sort.Strings(j.Targets)
		jobs[j.Name] = j
	}

	topology.WalkSilent(func(node *infra.Node) {
		if node.Server {
			add(&job{
				Name:        "consul-server--" + node.Name,
				MetricsPath: "/v1/agent/metrics",
				Params: map[string][]string{
					"format": {"prometheus"},
					"token":  {cfg.AgentMasterToken},
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
				Name:        "consul-client--" + node.Name,
				MetricsPath: "/v1/agent/metrics",
				Params: map[string][]string{
					"format": {"prometheus"},
					"token":  {cfg.AgentMasterToken},
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
					Name:        "mesh-gateway--" + node.Name,
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
					Name:        node.Service.ID.Name + "-proxy--" + node.Name,
					MetricsPath: "/metrics",
					Targets: []string{
						net.JoinHostPort(node.LocalAddress(), "9102"),
					},
					Labels: []kv{
						{"cluster", node.Cluster},
						{"namespace", node.Service.ID.Namespace},
						{"partition", node.Service.ID.Partition},
						{"node", node.Name},
						{"role", node.Service.ID.Name + "-proxy"},
					},
				})
			}
		}
	})

	info := struct {
		Jobs []*job
	}{}
	for _, j := range jobs {
		info.Jobs = append(info.Jobs, j)
	}
	sort.Slice(info.Jobs, func(i, j int) bool {
		return info.Jobs[i].Name < info.Jobs[j].Name
	})

	return File("cache/prometheus.yml", Eval(prometheusConfigT, &info))
}

var prometheusConfigT = template.Must(template.ParseFS(content, "templates/prometheus-config.yml.tmpl"))
