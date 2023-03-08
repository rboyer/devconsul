package tfgen

import (
	"text/template"

	"github.com/rboyer/devconsul/cachestore"
	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

type terraformPod struct {
	PodName               string
	Node                  *infra.Node
	HCL                   string
	Labels                map[string]string
	EnterpriseLicensePath string
}

func GenerateNodeContainers(
	cfg *config.Config,
	topology *infra.Topology,
	cache *cachestore.Store,
	node *infra.Node,
	podContents bool,
) ([]Resource, error) {
	pod := terraformPod{
		PodName: node.PodName(),
		Node:    node,
		Labels:  map[string]string{
			//
		},
		EnterpriseLicensePath: cfg.EnterpriseLicensePath,
	}
	node.AddLabels(pod.Labels)

	var containers []Resource

	// pod placeholder container
	containers = append(containers, Eval(tfPauseT, &pod))

	if podContents {
		if node.IsAgent() {
			podHCL, err := GenerateAgentHCL(cfg, topology, node)
			if err != nil {
				return nil, err
			}
			pod.HCL = podHCL

			containers = append(containers, Eval(tfConsulT, &pod))
		}

		if node.RunsWorkloads() {
			if gwRes := GenerateMeshGatewayContainer(cfg, topology, pod.PodName, pod.Node); gwRes != nil {
				containers = append(containers, gwRes)
			}

			if resources := GeneratePingPongContainers(cfg, topology, pod.PodName, pod.Node); len(resources) > 0 {
				containers = append(containers, resources...)
			}
		}

		if node.Kind == infra.NodeKindInfra {
			resources, err := GenerateInfraContainers(cfg, topology, cache, pod.PodName, pod.Node)
			if err != nil {
				return nil, err
			}
			if len(resources) > 0 {
				containers = append(containers, resources...)
			}
		}
	}

	return containers, nil
}

var tfPauseT = template.Must(template.ParseFS(content, "templates/container-pause.tf.tmpl"))
var tfConsulT = template.Must(template.ParseFS(content, "templates/container-consul.tf.tmpl"))
