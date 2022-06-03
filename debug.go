package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-cleanhttp"
	"github.com/rboyer/safeio"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/consulfunc"
	"github.com/rboyer/devconsul/infra"
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

	return consulfunc.GetClient(c.topology.LeaderIP(config.PrimaryCluster, false), masterToken)
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

func (c *Core) RunCheckMesh() error {
	client := cleanhttp.DefaultClient()

	now := time.Now()

	c.topology.WalkSilent(func(n *infra.Node) {
		if n.Server || n.MeshGateway {
			return
		}
		addr := n.LocalAddress()

		logger := c.logger.Named(n.Name)
		logger = logger.With("addr", addr)

		logger.Info("Checking pingpong mesh instance")

		ppr, err := fetchPingPongPage(client, addr)
		if err != nil {
			logger.Error("fetching endpoint failed", "error", err)
			return
		}
		if ppr.Name != "" {
			logger = logger.Named("app__" + ppr.Name)
		}

		logger.Info("found application", "app", ppr.Name)
		if len(ppr.Pings) > 0 {
			evt := ppr.Pings[0]

			if evt.Err != "" {
				logger.Error("last ping", "error", evt.Err)
			} else {
				logger.Info("last ping",
					"started", prettyTime(now, evt.Start),
					"ended", prettyTime(now, evt.End))
			}
		}
		if len(ppr.Pongs) > 0 {
			evt := ppr.Pongs[0]

			if evt.Err != "" {
				logger.Error("last pong", "error", evt.Err)
			} else {
				logger.Info("last pong", "received",
					prettyTime(now, evt.Recv))
			}
		}
	})

	return nil
}

func prettyTime(now time.Time, t *time.Time) string {
	if t == nil {
		return "NO_DATA"
	}
	dur := now.Sub(*t)
	dur = dur.Round(time.Millisecond)
	return dur.String()
}

type PingPongResponse struct {
	Name  string `json:"name"`
	Pings []Ping `json:"pings"`
	Pongs []Ping `json:"pongs"`
}

type Ping struct {
	Err   string     `json:",omitempty"`
	Recv  *time.Time `json:",omitempty"`
	Start *time.Time `json:",omitempty"`
	End   *time.Time `json:",omitempty"`
	// DurSec int        `json:",omitempty"`
}

func fetchPingPongPage(client *http.Client, addr string) (*PingPongResponse, error) {
	resp, err := client.Get("http://" + addr + ":8080")
	if err != nil {
		return nil, err
	}
	if resp.Body == nil {
		return nil, errors.New("no response body")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var ppr PingPongResponse
	if err := json.Unmarshal(body, &ppr); err != nil {
		return nil, err
	}

	return &ppr, nil
}

func (c *Core) RunGRPCCheck() error {
	for _, srcCluster := range c.topology.Clusters() {
		client, err := consulfunc.GetClient(c.topology.LeaderIP(srcCluster.Name, false), c.masterToken)
		if err != nil {
			return fmt.Errorf("error creating client for cluster=%s: %w", srcCluster.Name, err)
		}
		for _, dstCluster := range c.topology.Clusters() {
			c.logger.Info("Checking gRPC",
				"src-cluster", srcCluster.Name,
				"dst-cluster", dstCluster.Name)
			nodes, _, err := client.Health().Service("consul", "", false, &api.QueryOptions{
				UseCache:   true,
				AllowStale: true,
				Datacenter: dstCluster.Name,
			})
			if err != nil {
				c.logger.Error("...ERROR",
					"src-cluster", srcCluster.Name,
					"dst-cluster", dstCluster.Name,
					"error", err)
			} else {
				c.logger.Error("...OK",
					"src-cluster", srcCluster.Name,
					"dst-cluster", dstCluster.Name,
					"nodes", len(nodes))
			}
		}
	}
	return nil
}
