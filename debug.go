package main

import (
	"fmt"
	"strings"

	"github.com/hashicorp/go-cleanhttp"
	"github.com/rboyer/safeio"

	"github.com/hashicorp/consul/api"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/consulfunc"
)

var knownConfigEntryKinds = []string{
	api.ProxyDefaults,
	api.ServiceDefaults,
	api.ServiceResolver,
	api.ServiceSplitter,
	api.ServiceRouter,
	api.TerminatingGateway,
	api.IngressGateway,
	api.ServiceIntentions,
	api.MeshConfig,
	api.ExportedServices,
}

func (c *Core) RunDebugListConfigs() error {
	client, err := c.debugPrimaryClient()
	if err != nil {
		return err
	}
	_ = client

	configClient := client.ConfigEntries()
	for _, kind := range knownConfigEntryKinds {
		entries, _, err := configClient.List(kind, nil)
		if err != nil {
			if strings.Contains(err.Error(), "invalid config entry kind") {
				continue
			}
			return err
		}
		for _, entry := range entries {
			if c.config.EnterpriseEnabled {
				fmt.Printf("%s/%s/%s\n", entry.GetKind(), entry.GetNamespace(), entry.GetName())
			} else {
				fmt.Printf("%s/%s\n", entry.GetKind(), entry.GetName())
			}
		}
	}
	return nil
}

func (c *Core) debugPrimaryClient() (*api.Client, error) {
	masterToken, err := c.cache.LoadValue("master-token")
	if err != nil {
		return nil, err
	}

	return consulfunc.GetClient(c.topology.LeaderIP(config.PrimaryDC, false), masterToken)
}

func (c *Core) RunDebugSaveGrafana() error {
	grafanaURL := "http://localhost:3000/api/dashboards/db/devconsul-dashboard | jq .dashboard"

	client := cleanhttp.DefaultClient()

	resp, err := client.Get(grafanaURL)
	if err != nil {
		return err
	}
	if resp.Body == nil {
		return fmt.Errorf("body not populated")
	}
	defer resp.Body.Close()

	_, err = safeio.WriteToFile(resp.Body, "connect_service_dashboard.json", 0644)
	if err != nil {
		return err
	}

	c.logger.Info("Updated 'connect_service_dashboard.json' locally, you'll still have to commit it")

	return nil
}
