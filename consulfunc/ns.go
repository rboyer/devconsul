package consulfunc

import (
	"github.com/hashicorp/consul/api"
)

func ListNamespaces(client *api.Client, partition string) ([]*api.Namespace, error) {
	nsClient := client.Namespaces()
	nsList, _, err := nsClient.List(&api.QueryOptions{
		Partition: partition,
	})
	if err != nil {
		return nil, err
	}
	return nsList, nil
}

func ListNamespaceNames(client *api.Client, partition string) ([]string, error) {
	nsList, err := ListNamespaces(client, partition)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, ns := range nsList {
		out = append(out, ns.Name)
	}
	return out, nil
}

func TenantQueryOptionsList(client *api.Client, enterprise bool) ([]*api.QueryOptions, error) {
	if !enterprise {
		return []*api.QueryOptions{{}}, nil
	}

	partitions, err := ListPartitionNames(client)
	if err != nil {
		return nil, err
	}

	var out []*api.QueryOptions
	for _, partition := range partitions {
		namespaces, err := ListNamespaceNames(client, partition)
		if err != nil {
			return nil, err
		}

		for _, ns := range namespaces {
			out = append(out, &api.QueryOptions{
				Partition: partition,
				Namespace: ns,
			})
		}
	}
	return out, nil
}
