package consulfunc

import (
	"github.com/hashicorp/consul/api"

	"github.com/rboyer/devconsul/util"
)

func ListAllNodes(client *api.Client, dc string, enterprise bool) ([]*api.Node, error) {
	return ListAllNodesWithFilter(client, dc, enterprise, "")
}

func ListAllNodesWithFilter(client *api.Client, dc string, enterprise bool, filter string) ([]*api.Node, error) {
	queryOptionList, err := PartitionQueryOptionsList(client, enterprise)
	if err != nil {
		return nil, err
	}

	cc := client.Catalog()

	var out []*api.Node
	for _, queryOpts := range queryOptionList {
		queryOpts.Datacenter = dc
		queryOpts.Filter = filter
		nodes, _, err := cc.Nodes(queryOpts)
		if err != nil {
			return nil, err
		}
		out = append(out, nodes...)
	}
	return out, nil
}

func ListAllServices(client *api.Client, enterprise bool) ([]util.Identifier, error) {
	queryOptionList, err := TenantQueryOptionsList(client, enterprise)
	if err != nil {
		return nil, err
	}

	cc := client.Catalog()

	var out []util.Identifier
	for _, queryOpts := range queryOptionList {
		names, _, err := cc.Services(queryOpts)
		if err != nil {
			return nil, err
		}

		for name := range names {
			sid := util.NewIdentifier(name, queryOpts.Namespace, queryOpts.Partition)
			out = append(out, sid)
		}
	}
	return out, nil
}
