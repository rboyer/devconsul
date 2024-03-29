package consulfunc

import (
	"fmt"
	"strings"

	"github.com/hashicorp/consul/api"
)

type ConfigKindName struct {
	Kind      string
	Name      string
	Namespace string
	Partition string
}

var kinds = []string{
	api.MeshConfig,
	api.ServiceIntentions,
	api.IngressGateway,
	api.TerminatingGateway,
	api.ServiceRouter,
	api.ServiceSplitter,
	api.ServiceResolver,
	api.ServiceDefaults,
	api.ProxyDefaults,
	api.ExportedServices,
}

func ListAllConfigEntries(client *api.Client, enterprise bool) (map[ConfigKindName]api.ConfigEntry, error) {
	ce := client.ConfigEntries()

	queryOptionList, err := PartitionQueryOptionsList(client, enterprise)
	if err != nil {
		return nil, fmt.Errorf("error listing partitions: %w", err)
	}

	m := make(map[ConfigKindName]api.ConfigEntry)
	for _, kind := range kinds {
		for _, queryOpts := range queryOptionList {
			entries, _, err := ce.List(kind, queryOpts)
			if err != nil {
				if strings.Contains(err.Error(), "invalid config entry kind") {
					continue // version skew
				}
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
