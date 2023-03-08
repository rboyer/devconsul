package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-hclog"

	"github.com/rboyer/devconsul/consulfunc"
	"github.com/rboyer/devconsul/structs"
	"github.com/rboyer/devconsul/util"
)

const (
	checkTimeout = 2 * time.Second
)

type NodeID = util.Identifier2
type ServiceID = util.Identifier

func init() {
	allCommands["catalog-sync"] = &catalogSyncCommand{}
}

type catalogSyncCommand struct {
	logger hclog.Logger
	conf   *structs.CatalogDefinition
	client *api.Client

	// lazy
	dialer *net.Dialer
	last   healthResults

	cluster        string
	flagConfig     string
	flagConfigFile string
	flagFreq       time.Duration
	enterprise     bool
	token          string
	tokenFile      string
	rawConsulIPs   string

	consulIPs []string
}

func (c *catalogSyncCommand) RegisterFlags() {
	flag.StringVar(&c.flagConfig, "config", "", "json configuration")
	flag.StringVar(&c.flagConfigFile, "config-file", "", "json configuration file")
	flag.DurationVar(&c.flagFreq, "freq", 1*time.Second, "frequency of checks")
	flag.StringVar(&c.token, "token", "", "consul token")
	flag.StringVar(&c.tokenFile, "token-file", "", "consul token file")
	flag.StringVar(&c.rawConsulIPs, "consul-ip", "", "consul address")
	flag.BoolVar(&c.enterprise, "enterprise", false, "should we care about enterprise")
	flag.StringVar(&c.cluster, "cluster", "", "cluster name")
}

func pickRandomIP(ips []string) string {
	switch len(ips) {
	case 0:
		return ""
	case 1:
		return ips[0]
	default:
		idx := rand.Intn(len(ips))
		return ips[idx]
	}
}

func (c *catalogSyncCommand) Run(logger hclog.Logger) error {
	c.logger = logger

	if c.flagFreq <= 0 {
		return fmt.Errorf("freq flag is invalid: %v", c.flagFreq)
	}
	if c.rawConsulIPs == "" {
		return errors.New("missing required 'consul-ip' argument")
	} else {
		c.consulIPs = strings.Split(c.rawConsulIPs, ",")
		c.rawConsulIPs = ""
	}
	if c.cluster == "" {
		return errors.New("missing required 'cluster' argument")
	}

	if c.flagConfig == "" && c.flagConfigFile == "" {
		return errors.New("one of 'config' or 'config-file' is required")
	}
	if c.flagConfig != "" && c.flagConfigFile != "" {
		return errors.New("ONLY one of 'config' or 'config-file' is required")
	}

	if c.token != "" && c.tokenFile != "" {
		return errors.New("ONLY one of 'token' or 'token-file' can be set at once")
	}

	if c.tokenFile != "" {
		for {
			_, err := os.Stat(c.tokenFile)
			if err == nil {
				break
			} else if os.IsNotExist(err) {
				c.logger.Info("token-file not ready yet")
				time.Sleep(250 * time.Millisecond)
			} else if err != nil {
				return fmt.Errorf("'token-file' is not readable %q: %w", c.tokenFile, err)
			}
		}

		b, err := os.ReadFile(c.tokenFile)
		if err != nil {
			return fmt.Errorf("error reading token file %q: %w", c.tokenFile, err)
		}
		c.token = strings.TrimSpace(string(b))
		c.tokenFile = ""
	}

	if c.flagConfigFile != "" {
		b, err := os.ReadFile(c.flagConfigFile)
		if err != nil {
			return fmt.Errorf("error reading from config file %q: %w", c.flagConfigFile, err)
		}
		c.flagConfig = string(b)
		c.flagConfigFile = ""
	}

	c.conf = &structs.CatalogDefinition{}
	if err := json.Unmarshal([]byte(c.flagConfig), c.conf); err != nil {
		return fmt.Errorf("error parsing provided config: %w", err)
	}

	serverIP := pickRandomIP(c.consulIPs)
	c.logger.Info("pinning to server", "ip", serverIP)

	var err error
	c.client, err = consulfunc.GetClient(serverIP, c.token)
	if err != nil {
		return fmt.Errorf("could not create consul api client: %w", err)
	}

	if err := c.initialSync(); err != nil {
		return fmt.Errorf("initial catalog sync failed: %w", err)
	}

	return c.detectAndSyncHealth()

}

func (c *catalogSyncCommand) initialSync() error {
	c.logger.Info("syncing initial catalog data",
		"num_nodes", len(c.conf.Nodes),
		"num_services", len(c.conf.Services),
		"num_proxies", len(c.conf.Proxies),
	)

	curr, err := c.dumpCurrentCatalog(c.cluster)
	if err != nil {
		return fmt.Errorf("error dumping current catalog state: %w", err)
	}

	// register missing nodes
	expect := make(map[NodeID]map[ServiceID]struct{})
	for _, n := range c.conf.Nodes {
		nid := n.ID()

		// register synthetic node
		if _, err := c.client.Catalog().Register(n.ToAPI(c.enterprise), nil); err != nil {
			return fmt.Errorf("error registering virtual node %s: %w", nid, err)
		}
		expect[nid] = make(map[ServiceID]struct{})

		c.logger.Info("agentless node created",
			"node", n.Node,
			"partition", n.Partition,
		)
	}

	// register missing services
	for _, svc := range c.conf.Services {
		sid := svc.ID()
		nid := svc.NodeID()

		if _, err := c.client.Catalog().Register(svc.ToAPI(c.enterprise), nil); err != nil {
			return fmt.Errorf("error registering service %s to node %s: %w", sid, nid, err)
		}

		expect[nid][sid] = struct{}{}

		c.logger.Info("agentless service created",
			"service", sid.Name,
			"node", nid.Name,
			"namespace", sid.Namespace,
			"partition", sid.Partition,
		)
	}

	// register missing proxies
	for _, svc := range c.conf.Proxies {
		sid := svc.ID()
		nid := svc.NodeID()

		if _, err := c.client.Catalog().Register(svc.ToAPI(c.enterprise), nil); err != nil {
			return fmt.Errorf("error registering proxy for service %s to node %s: %w", sid, nid, err)
		}

		expect[nid][sid] = struct{}{}

		c.logger.Info("agentless proxy created",
			// "service", n.Service.ID.Name+"-sidecar-proxy",
			"service", sid.Name,
			"node", nid.Name,
			"namespace", sid.Namespace,
			"partition", sid.Partition,
		)
	}

	// remove all strays
	for nid, m := range curr {
		expectM, ok := expect[nid]
		if !ok {
			_, err := c.client.Catalog().Deregister(&api.CatalogDeregistration{
				Node:      nid.Name,
				Partition: nid.Partition,
			}, nil)
			if err != nil {
				return fmt.Errorf("error deregistering virtual node %s: %w", nid, err)
			}
			c.logger.Info("agentless node removed",
				"node", nid.Name,
				"partition", nid.Partition,
			)
			continue
		}

		for sid := range m {
			if _, ok := expectM[sid]; !ok {
				_, err := c.client.Catalog().Deregister(&api.CatalogDeregistration{
					Node:      nid.Name,
					Namespace: sid.Namespace,
					Partition: sid.Partition,
					ServiceID: sid.Name,
				}, nil)
				if err != nil {
					return fmt.Errorf("error deregistering virtual service %s from node %s: %w", sid, nid, err)
				}

				c.logger.Info("agentless service removed",
					"service", sid.Name,
					"node", nid.Name,
					"namespace", sid.Namespace,
					"partition", sid.Partition,
				)
			}
		}
	}

	return nil
}

func (c *catalogSyncCommand) dumpCurrentCatalog(cluster string) (map[NodeID]map[ServiceID]struct{}, error) {
	// dump virtual nodes
	rawNodes, err := consulfunc.ListAllNodesWithFilter(c.client, cluster, c.enterprise, `"devconsul-virtual" in Meta`)
	if err != nil {
		return nil, err
	}

	curr := make(map[NodeID]map[ServiceID]struct{})
	for _, n := range rawNodes {
		nid := util.NewIdentifier2(n.Node, n.Partition)

		m, ok := curr[nid]
		if !ok {
			m = make(map[ServiceID]struct{})
			curr[nid] = m
		}

		rawSvc, _, err := c.client.Catalog().NodeServiceList(n.Node, &api.QueryOptions{
			Partition: n.Partition,
			Namespace: "*",
		})
		if err != nil {
			return nil, err
		}

		for _, svc := range rawSvc.Services {
			sid := util.NewIdentifier(svc.Service, svc.Namespace, svc.Partition)
			m[sid] = struct{}{}
		}
	}
	// $ curl -sLi --get 10.0.1.11:8500/v1/catalog/nodes?token=root --data-urlencode 'filter="devconsul-virtual" in Meta'

	return curr, nil
}

func (c *catalogSyncCommand) detectAndSyncHealth() error {
	for {
		c.detectAndSyncHealthOnce()
		time.Sleep(c.flagFreq)
	}
}

type healthResults struct {
	data map[NodeID]map[ServiceID]string
}

func (h *healthResults) getResult(nid NodeID, sid ServiceID) string {
	if h.data == nil {
		return ""
	}

	if m, ok := h.data[nid]; ok {
		if v, ok := m[sid]; ok {
			return v
		}
	}
	return ""
}

func (h *healthResults) setResult(nid NodeID, sid ServiceID, value string) {
	if h.data == nil {
		h.data = make(map[NodeID]map[ServiceID]string)
	}

	m, ok := h.data[nid]
	if !ok {
		m = make(map[ServiceID]string)
		h.data[nid] = m
	}
	m[sid] = value
}

func (c *catalogSyncCommand) detectAndSyncHealthOnce() {
	for _, svc := range c.conf.Services {
		if svc.TCPCheck == "" || svc.CheckID == "" {
			continue
		}

		logger := c.logger.With(
			"node", svc.NodeID().String(),
			"service", svc.ID().String(),
		)

		var (
			nid = svc.NodeID()
			sid = svc.ID()
		)

		lastResult := c.last.getResult(nid, sid)

		if err := c.checkTCP(svc.TCPCheck); err != nil {
			if lastResult != api.HealthCritical {
				logger.Warn("health check status is now failing", "p", lastResult, "n", api.HealthCritical)

				if err := c.syncHealth(svc, api.HealthCritical); err != nil {
					logger.Error("could not update catalog for health status change", "error", err)
					continue
				}

				c.last.setResult(nid, sid, api.HealthCritical)
			}
		} else {
			if lastResult != api.HealthPassing {
				logger.Info("health check status is now passing", "p", lastResult, "n", api.HealthPassing)

				if err := c.syncHealth(svc, api.HealthPassing); err != nil {
					logger.Error("could not update catalog for health status change", "error", err)
					continue
				}

				c.last.setResult(nid, sid, api.HealthPassing)
			}
		}
	}
}

func (c *catalogSyncCommand) syncHealth(svc *structs.CatalogService, status string) error {
	reg := svc.ToAPI(c.enterprise)
	reg.Check.Status = status

	_, err := c.client.Catalog().Register(reg, nil)
	return err
}

func (c *catalogSyncCommand) checkTCP(addr string) error {
	if c.dialer == nil {
		// Create the socket dialer
		c.dialer = &net.Dialer{
			Timeout: checkTimeout,
		}
	}

	conn, err := c.dialer.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp check failed: %w", err)
	}
	conn.Close()
	return nil
}
