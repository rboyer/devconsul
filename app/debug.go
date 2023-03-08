package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-cleanhttp"
	"github.com/rboyer/safeio"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/consulfunc"
	"github.com/rboyer/devconsul/grafana"
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

func (a *App) RunDumpLogs() error {
	// This only makes sense to run after you've configured it once.
	if err := checkHasInitRunOnce(); err != nil {
		return err
	}

	if err := os.RemoveAll("logs"); err != nil {
		return fmt.Errorf("problem clearing logs directory: %w", err)
	}

	if err := os.MkdirAll("logs", 0755); err != nil {
		return fmt.Errorf("could not create logs output directory: %w", err)
	}

	logDockerCmd := func(fn string, args []string) error {
		var err error
		fn, err = filepath.Abs(fn)
		if err != nil {
			return err
		}
		w, err := safeio.OpenFile(fn, 0644)
		if err != nil {
			return err
		}
		defer w.Close()

		if err := a.runner.DockerExec(args, w); err != nil {
			return err
		}

		return w.Commit()
	}

	writeLogs := func(c string) error {
		fn := filepath.Join("logs", c+".log")
		if err := logDockerCmd(fn, []string{"logs", c}); err != nil {
			return err
		}

		a.logger.Info("captured docker logs", "container", c, "path", fn)

		return nil
	}

	dumpRoute := func(c string) error {
		fn := filepath.Join("logs", c+".route.txt")
		args := []string{
			"exec",
			c,
			"route", "-n",
			// d exec dc1-server1 route -n
		}
		if err := logDockerCmd(fn, args); err != nil {
			return err
		}

		a.logger.Info("captured docker route table", "container", c, "path", fn)

		return nil
	}

	doStuff := func(c string) error {
		if err := writeLogs(c); err != nil {
			return err
		}
		if err := dumpRoute(c); err != nil {
			return err
		}
		return nil
	}

	a.topology.WalkSilent(func(n *infra.Node) {
		var containers []string

		containers = append(containers, n.Name)

		if n.MeshGateway {
			containers = append(
				containers,
				n.Name+"-mesh-gateway",
			)
		}
		if n.Service != nil {
			containers = append(
				containers,
				n.Name+"-"+n.Service.ID.Name+"-sidecar-proxy",
			)
		}

		for _, c := range containers {
			if err := doStuff(c); err != nil {
				switch {
				case strings.Contains(err.Error(), `Error response from daemon: No such container:`):
				case strings.Contains(err.Error(), `Error: No such container:`):
				default:
					a.logger.Error("could not capture docker logs", "container", c, "error", err)
				}
			}
		}
	})

	return nil
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

type grafanaDashboardListItem struct {
	ID        int      `json:"id"`
	UID       string   `json:"uid"`
	Title     string   `json:"title"`
	URI       string   `json:"uri"`
	URL       string   `json:"url"`
	Slug      string   `json:"slug"`
	Type      string   `json:"type"`
	Tags      []string `json:"tags"`
	IsStarred bool     `json:"isStarred"`
}

func (c *Core) RunDebugSaveGrafana() error {
	client, err := grafana.NewClient(c.logger.Named("grafana"))
	if err != nil {
		return err
	}

	list, err := client.ListDashboards()
	if err != nil {
		return err
	}

	for _, item := range list {
		name := strings.TrimPrefix(item.URI, "db/")
		fileName := filepath.Join("dashboards", name+".json")

		if err := c.runDebugSaveGrafana(client, item.UID, fileName); err != nil {
			return fmt.Errorf("error saving board %q: %w", item.Title, err)
		}
	}

	return nil
}

func (c *Core) restoreGrafana() error {
	names, err := os.ReadDir("dashboards")
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("dashboards directory is missing: %w", err)
	}

	client, err := grafana.NewClient(c.logger.Named("grafana"))
	if err != nil {
		return err
	}

	for _, fi := range names {
		if !strings.HasSuffix(fi.Name(), ".json") {
			continue
		}
		full := filepath.Join("dashboards", fi.Name())
		c.logger.Info("found file", "name", full)

		b, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("error reading dashboard file %q: %w", full, err)
		}

		var raw map[string]any
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}

		delete(raw, "id")

		exists, _, err := client.GetRawDashboard(raw["uid"].(string))
		if err != nil {
			return err
		}

		if !exists {
			c.logger.Info("restoring dashboard to grafana", "file", full)
			if err := client.UpsertRawDashboard(raw); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Core) runDebugSaveGrafana(client *grafana.Client, uid, fileName string) error {
	exists, rawBoard, err := client.GetRawDashboard(uid)
	if err != nil {
		return err
	} else if !exists {
		return errors.New("does not exist")
	}

	f, err := safeio.OpenFile(fileName, 0644)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if err := enc.Encode(rawBoard); err != nil {
		return err
	}

	if err := f.Commit(); err != nil {
		return err
	}

	c.logger.Info("Updated '" + fileName + "' locally, you'll still have to commit it")

	return nil
}

func (c *Core) RunCheckMesh() error {
	client := cleanhttp.DefaultClient()

	var stopCh <-chan time.Time
	if c.timeout > 0 {
		stopCh = time.After(c.timeout)
	}

	successMap := make(map[string]map[string]struct{})
	for {
		select {
		case <-stopCh:
			return errors.New("did not complete")
		default:
		}

		anyFailed := false
		c.topology.WalkSilent(func(n *infra.Node) {
			if !n.RunsWorkloads() || n.MeshGateway || n.Service == nil {
				return
			}
			addr := n.LocalAddress()
			sid := n.Service.ID.String()

			nodeSuccessMap, ok := successMap[n.Name]
			if !ok {
				nodeSuccessMap = make(map[string]struct{})
				successMap[n.Name] = nodeSuccessMap
			}

			if _, ok := nodeSuccessMap[sid]; ok {
				return
			}

			logger := c.logger.With(
				"node", n.Name,
				"service", sid,
				"addr", addr,
			)

			// logger.Info("Checking pingpong mesh instance")

			status, err := fetchPingHealthz(client, addr)
			if err != nil {
				logger.Error("fetching endpoint failed", "error", err)
				anyFailed = true
				return
			}
			if status == "OK" {
				logger.Info("last ping", "status", status)
				nodeSuccessMap[sid] = struct{}{}
			} else {
				logger.Error("last ping", "status", status)
				anyFailed = true
			}
		})
		if !anyFailed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.logger.Info("mesh check complete", "status", "OK")

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

func fetchPingHealthz(client *http.Client, addr string) (string, error) {
	resp, err := client.Get("http://" + addr + ":8080/pinghealthz")
	if err != nil {
		return "", err
	}
	if resp.Body == nil {
		return "", errors.New("no response body")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(body)), nil
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
