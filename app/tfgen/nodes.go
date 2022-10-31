package tfgen

import (
	"text/template"

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

func GenerateAgentContainers(
	cfg *config.Config,
	topology *infra.Topology,
	node *infra.Node,
	podContents bool,
) ([]Resource, error) {
	podName := node.Name + "-pod"

	podHCL, err := GenerateAgentHCL(cfg, topology, node)
	if err != nil {
		return nil, err
	}

	pod := terraformPod{
		PodName: podName,
		Node:    node,
		HCL:     podHCL,
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
		containers = append(containers, Eval(tfConsulT, &pod))

		if gwRes := GenerateMeshGatewayContainer(cfg, topology, podName, pod.Node); gwRes != nil {
			containers = append(containers, gwRes)
		}

		if resources := GeneratePingPongContainers(cfg, podName, pod.Node); len(resources) > 0 {
			containers = append(containers, resources...)
		}
	}

	return containers, nil
}

var tfPauseT = template.Must(template.ParseFS(content, "templates/container-pause.tf.tmpl"))
var tfConsulT = template.Must(template.ParseFS(content, "templates/container-consul.tf.tmpl"))
