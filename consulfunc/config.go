package consulfunc

import (
	"github.com/hashicorp/consul/api"
)

type ConfigKindName struct {
	Kind string
	Name string
}

var kinds = []string{
	api.ServiceDefaults,
	api.ProxyDefaults,
	api.ServiceRouter,
	api.ServiceSplitter,
	api.ServiceResolver,
}

func ListAllConfigEntries(client *api.Client) (map[ConfigKindName]api.ConfigEntry, error) {
	ce := client.ConfigEntries()

	m := make(map[ConfigKindName]api.ConfigEntry)
	for _, kind := range kinds {
		entries, _, err := ce.List(kind, nil)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			ckn := ConfigKindName{
				Kind: entry.GetKind(),
				Name: entry.GetName(),
			}
			m[ckn] = entry
		}
	}

	return m, nil
}
