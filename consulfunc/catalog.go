package consulfunc

import "github.com/hashicorp/consul/api"

func ListAllNodes(client *api.Client, dc string, enterprise, partitionsDisabled bool) ([]*api.Node, error) {
	queryOptionList, err := PartitionQueryOptionsList(client, enterprise, partitionsDisabled)
	if err != nil {
		return nil, err
	}

	cc := client.Catalog()

	var out []*api.Node
	for _, queryOpts := range queryOptionList {
		queryOpts.Datacenter = dc
		nodes, _, err := cc.Nodes(queryOpts)
		if err != nil {
			return nil, err
		}
		out = append(out, nodes...)
	}
	return out, nil
}
