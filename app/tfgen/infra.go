package tfgen

import (
	"strings"
	"text/template"

	"github.com/rboyer/devconsul/cachestore"
	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
	"github.com/rboyer/devconsul/util"
)

type catalogSyncInfo struct {
	PodName   string
	NodeName  string
	Args      []string
	HashValue string
}

func GenerateInfraContainers(
	config *config.Config,
	topology *infra.Topology,
	cache *cachestore.Store,
	podName string,
	node *infra.Node,
) ([]Resource, error) {
	if node.Kind != infra.NodeKindInfra {
		return nil, nil
	}

	filename := "catalog_def." + node.Cluster + ".json"
	hv, err := util.HashFile(cache.GetPathToStringFile(filename))
	if err != nil {
		return nil, err
	}

	info := catalogSyncInfo{
		PodName:   node.PodName(),
		NodeName:  node.Name,
		HashValue: hv,
		Args: []string{
			"-cluster", node.Cluster,
			"-config-file", "/secrets/" + filename,
			"-consul-ip", strings.Join(topology.ServerIPs(node.Cluster), ","),
		},
	}

	if config.EnterpriseEnabled {
		info.Args = append(info.Args, "-enterprise")
	}

	if !config.SecurityDisableACLs {
		// TODO: switch this to its own token
		info.Args = append(info.Args, "-token-file", "/secrets/master-token.val")
	}

	res := Eval(tfCatalogSyncT, &info)

	return []Resource{res}, nil
}

var tfCatalogSyncT = template.Must(template.ParseFS(content, "templates/container-catalog-sync.tf.tmpl"))
