package consulfunc

import (
	"context"

	"github.com/hashicorp/consul/api"
)

func ListPartitions(client *api.Client) ([]*api.Partition, error) {
	partClient := client.Partitions()
	partitions, _, err := partClient.List(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	return partitions, nil
}

func ListPartitionNames(client *api.Client) ([]string, error) {
	partitions, err := ListPartitions(client)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, part := range partitions {
		out = append(out, part.Name)
	}
	return out, nil
}

func PartitionQueryOptionsList(client *api.Client, enterprise, partitionsDisabled bool) ([]*api.QueryOptions, error) {
	if !enterprise || partitionsDisabled {
		return []*api.QueryOptions{&api.QueryOptions{}}, nil
	}

	names, err := ListPartitionNames(client)
	if err != nil {
		return nil, err
	}
	var out []*api.QueryOptions
	for _, name := range names {
		out = append(out, &api.QueryOptions{Partition: name})
	}
	return out, nil
}
