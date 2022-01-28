package consulfunc

import (
	"fmt"

	"github.com/hashicorp/consul/api"
)

type ConfigKindName struct {
	Kind      string
	Name      string
	Namespace string
	Partition string
}

var kinds = []string{
	api.ServiceDefaults,
	api.ProxyDefaults,
	api.ServiceRouter,
	api.ServiceSplitter,
	api.ServiceResolver,
}

func ListAllConfigEntries(client *api.Client, enterprise, partitionsDisabled bool) (map[ConfigKindName]api.ConfigEntry, error) {
	ce := client.ConfigEntries()

	queryOptionList, err := PartitionQueryOptionsList(client, enterprise, partitionsDisabled)
	if err != nil {
		return nil, fmt.Errorf("error listing partitions: %w", err)
	}

	m := make(map[ConfigKindName]api.ConfigEntry)
	for _, kind := range kinds {
		for _, queryOpts := range queryOptionList {
			entries, _, err := ce.List(kind, queryOpts)
			if err != nil {
				return nil, err
			}

			for _, entry := range entries {
				ckn := ConfigKindName{
					Kind:      entry.GetKind(),
					Name:      entry.GetName(),
					Namespace: orDefault(entry.GetNamespace(), "default"),
					Partition: orDefault(entry.GetPartition(), "default"),
				}
				m[ckn] = entry
			}
		}
	}

	return m, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
