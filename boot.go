package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/consulfunc"
	"github.com/rboyer/devconsul/infra"
	"github.com/rboyer/devconsul/util"
)

type BootInfo struct {
	primaryOnly bool

	masterToken         string
	clients             map[string]*api.Client
	replicationSecretID string

	tokens map[string]string
}

func (c *Core) runBoot(primaryOnly bool) error {
	if err := checkHasRunOnce("init"); err != nil {
		return err
	}

	c.primaryOnly = primaryOnly

	if c.primaryOnly {
		c.logger.Info("only bootstrapping the primary cluster", "cluster", config.PrimaryCluster)
	}

	var err error

	c.clients = make(map[string]*api.Client)
	for _, cluster := range c.topology.Clusters() {
		if c.primaryOnly && !cluster.Primary {
			continue
		}
		c.clients[cluster.Name], err = consulfunc.GetClient(c.topology.LeaderIP(cluster.Name, false), "" /*no token yet*/)
		if err != nil {
			return fmt.Errorf("error creating initial bootstrap client for cluster=%s: %w", cluster.Name, err)
		}

		c.waitForLeader(cluster.Name)
	}

	if c.config.SecurityDisableACLs {
		if err := c.cache.DelValue("master-token"); err != nil {
			return err
		}
		c.masterToken = ""
	} else {
		if err := c.bootstrap(c.primaryClient()); err != nil {
			return fmt.Errorf("bootstrap: %v", err)
		}
	}

	// now we have master token set we can do anything
	c.clients[config.PrimaryCluster], err = consulfunc.GetClient(c.topology.LeaderIP(config.PrimaryCluster, false), c.masterToken)
	if err != nil {
		return fmt.Errorf("error creating final client for cluster=%s: %v", config.PrimaryCluster, err)
	}

	switch c.topology.LinkMode {
	case infra.ClusterLinkModeFederate:
		if err := c.initPrimaryCluster(config.PrimaryCluster); err != nil {
			return err
		}
	case infra.ClusterLinkModePeer:
		for _, cluster := range c.topology.Clusters() {
			if err := c.initPrimaryCluster(cluster.Name); err != nil {
				return fmt.Errorf("initPrimaryCluster[%q]: %w", cluster.Name, err)
			}
		}
	}

	if err := c.writeServiceRegistrationFiles(); err != nil {
		return fmt.Errorf("writeServiceRegistrationFiles: %w", err)
	}

	if !c.primaryOnly {
		if c.topology.LinkWithFederation() && len(c.topology.Clusters()) > 1 {
			if err := c.initSecondaryDCs(); err != nil {
				return err
			}
		}
	}

	if c.topology.LinkWithPeering() {
		if err := c.peerClusters(); err != nil {
			return fmt.Errorf("peerClusters: %w", err)
		}
	}

	if err := c.cache.SaveValue("ready", "1"); err != nil {
		return err
	}

	return nil
}

func (c *Core) peerClusters() error {
	primaryClient := c.primaryClient()
	pc := primaryClient.Peerings()
	for _, cluster := range c.topology.Clusters() {
		if cluster.Name == config.PrimaryCluster {
			continue // skip
		}

		resp, _, err := pc.GenerateToken(context.Background(), api.PeeringGenerateTokenRequest{
			PeerName: "peer-" + cluster.Name,
		}, nil)
		if err != nil {
			return fmt.Errorf("error generating peering token for %q from %q: %w",
				cluster.Name, config.PrimaryCluster, err)
		}

		token := resp.PeeringToken

		targetClient := c.clientForCluster(cluster.Name)

		tpc := targetClient.Peerings()

		_, _, err = tpc.Initiate(context.Background(), api.PeeringInitiateRequest{
			PeerName:     "peer-" + config.PrimaryCluster,
			PeeringToken: token,
		}, nil)
		if err != nil {
			return fmt.Errorf("error initiating peering with token in %q to %q: %w",
				cluster.Name, config.PrimaryCluster, err)
		}
	}
	return nil
}

func (c *Core) waitForCrossDatacenterKV(fromCluster, toCluster string) {
	client := c.clients[fromCluster]

	for {
		_, err := client.KV().Put(&api.KVPair{
			Key:   "test-from-" + fromCluster + "-to-" + toCluster,
			Value: []byte("payload-for-" + fromCluster + "-to-" + toCluster),
		}, &api.WriteOptions{
			Datacenter: toCluster,
		})

		if err == nil {
			c.logger.Info("kv write success",
				"from_cluster", fromCluster, "to_cluster", toCluster,
			)
			return
		}

		c.logger.Warn("kv write failed; wan not converged yet",
			"from_cluster", fromCluster, "to_cluster", toCluster,
		)
		time.Sleep(500 * time.Millisecond)
	}
}

func (c *Core) initPrimaryCluster(cluster string) error {
	var err error

	err = c.createPartitions(cluster)
	if err != nil {
		return fmt.Errorf("createPartitions[%s]: %w", cluster, err)
	}

	err = c.createNamespaces(cluster)
	if err != nil {
		return fmt.Errorf("createNamespaces[%s]: %w", cluster, err)
	}

	if !c.config.SecurityDisableACLs {
		// TODO: peering
		err = c.createReplicationToken()
		if err != nil {
			return fmt.Errorf("createReplicationToken[%s]: %w", cluster, err)
		}

		err = c.createMeshGatewayToken()
		if err != nil {
			return fmt.Errorf("createMeshGatewayToken[%s]: %w", cluster, err)
		}

		err = c.createAgentTokens()
		if err != nil {
			return fmt.Errorf("createAgentTokens[%s]: %w", cluster, err)
		}
	}

	err = c.injectAgentTokensAndWaitForNodeUpdates(cluster)
	if err != nil {
		return fmt.Errorf("injectAgentTokensAndWaitForNodeUpdates[%s]: %w", cluster, err)
	}

	if !c.config.SecurityDisableACLs {
		// TODO: peering
		err = c.createAnonymousToken()
		if err != nil {
			return fmt.Errorf("createAnonymousPolicy[%s]: %w", cluster, err)
		}
	}

	err = c.writeCentralConfigs(cluster)
	if err != nil {
		return fmt.Errorf("writeCentralConfigs[%s]: %w", cluster, err)
	}

	if !c.config.SecurityDisableACLs {
		// TODO: peering
		if c.config.KubernetesEnabled {
			err = c.initializeKubernetes()
			if err != nil {
				return fmt.Errorf("initializeKubernetes[%s]: %w", cluster, err)
			}
		} else {
			err = c.createServiceTokens()
			if err != nil {
				return fmt.Errorf("createServiceTokens[%s]: %w", cluster, err)
			}
		}
	}

	return nil
}

func (c *Core) initSecondaryDCs() error {
	if !c.config.SecurityDisableACLs {
		err := c.injectReplicationToken()
		if err != nil {
			return fmt.Errorf("injectReplicationToken: %v", err)
		}
	}

	var err error
	for _, cluster := range c.topology.Clusters() {
		if cluster.Primary {
			continue
		}
		c.clients[cluster.Name], err = consulfunc.GetClient(c.topology.LeaderIP(cluster.Name, false), c.masterToken)
		if err != nil {
			return fmt.Errorf("error creating final client for cluster=%s: %v", cluster.Name, err)
		}

		err = c.injectAgentTokensAndWaitForNodeUpdates(cluster.Name)
		if err != nil {
			return fmt.Errorf("injectAgentTokensAndWaitForNodeUpdates[%s]: %v", cluster.Name, err)
		}
	}

	for _, cluster1 := range c.topology.Clusters() {
		for _, cluster2 := range c.topology.Clusters() {
			if cluster1 == cluster2 {
				continue
			}
			c.waitForCrossDatacenterKV(cluster1.Name, cluster2.Name)
		}
	}

	return nil
}

func (c *Core) primaryClient() *api.Client {
	return c.clients[config.PrimaryCluster]
}

func (c *Core) clientForCluster(cluster string) *api.Client {
	return c.clients[cluster]
}

func (c *Core) bootstrap(client *api.Client) error {
	// TODO: peering
	var err error
	c.masterToken, err = c.cache.LoadValue("master-token")
	if err != nil {
		return err
	}

	if c.masterToken == "" && c.config.InitialMasterToken != "" {
		c.masterToken = c.config.InitialMasterToken
		if err := c.cache.SaveValue("master-token", c.masterToken); err != nil {
			return err
		}
	}

	ac := client.ACL()

	if c.masterToken != "" {
	TRYAGAIN:
		// check to see if it works
		_, _, err := ac.TokenReadSelf(&api.QueryOptions{Token: c.masterToken})
		if err != nil {
			if strings.Index(err.Error(), "The ACL system is currently in legacy mode") != -1 {
				c.logger.Warn("system is rebooting", "error", err)
				time.Sleep(250 * time.Millisecond)
				goto TRYAGAIN
			}

			c.logger.Warn("master token doesn't work anymore", "error", err)
			return c.cache.DelValue("master-token")
		}
		c.logger.Info("current master token", "token", c.masterToken)
		return nil
	}

TRYAGAIN2:
	c.logger.Info("bootstrapping ACLs")
	tok, _, err := ac.Bootstrap()
	if err != nil {
		if strings.Index(err.Error(), "The ACL system is currently in legacy mode") != -1 {
			c.logger.Warn("system is rebooting", "error", err)
			time.Sleep(250 * time.Millisecond)
			goto TRYAGAIN2
		}
		return err
	}
	c.masterToken = tok.SecretID

	if err := c.cache.SaveValue("master-token", c.masterToken); err != nil {
		return err
	}

	c.logger.Info("current master token", "token", c.masterToken)

	return nil
}

func (c *Core) createPartitions(cluster string) error {
	if !c.config.EnterpriseEnabled {
		return nil
	}
	if c.config.EnterpriseDisablePartitions {
		return nil
	}

	var (
		client = c.clientForCluster(cluster)
		logger = c.logger.With("cluster", cluster)
	)

	partClient := client.Partitions()

	currentList, _, err := partClient.List(context.Background(), nil)
	if err != nil {
		return err
	}

	currentMap := make(map[string]struct{})
	for _, ap := range currentList {
		currentMap[ap.Name] = struct{}{}
	}

	for _, ap := range c.config.EnterprisePartitions {
		if _, ok := currentMap[ap.Name]; ok {
			delete(currentMap, ap.Name)
			continue
		}

		if ap.Name == "default" {
			continue // skip
		}

		obj := &api.Partition{
			Name: ap.Name,
		}

		_, _, err = partClient.Create(context.Background(), obj, nil)
		if err != nil {
			return fmt.Errorf("error creating partition %q: %w", ap.Name, err)
		}
		logger.Info("created partition", "partition", ap.Name)
	}

	delete(currentMap, "default")

	for ap := range currentMap {
		if _, err := partClient.Delete(context.Background(), ap, nil); err != nil {
			return err
		}
		logger.Info("deleted partition", "partition", ap)
	}

	return nil
}

func (c *Core) createNamespaces(cluster string) error {
	if !c.config.EnterpriseEnabled {
		return nil
	}
	for _, ap := range c.config.EnterprisePartitions {
		if err := c.createNamespacesForPartition(cluster, ap); err != nil {
			return fmt.Errorf("error creating namespaces in partition %q in cluster %q: %w", ap.Name, cluster, err)
		}
	}
	return nil
}

func (c *Core) createNamespacesForPartition(cluster string, ap *config.Partition) error {
	var (
		client = c.clientForCluster(cluster)
		logger = c.logger.With("cluster", cluster)
	)

	// Create a policy to allow super permissive catalog reads across namespace
	// boundaries.
	if err := c.createCrossNamespaceCatalogReadPolicy(cluster, ap); err != nil {
		return fmt.Errorf("createCrossNamespaceCatalogReadPolicy[%q]: %v", cluster, err)
	}

	nc := client.Namespaces()

	opts := &api.QueryOptions{Partition: ap.Name}

	currentList, _, err := nc.List(opts)
	if err != nil {
		return err
	}

	currentMap := make(map[string]struct{})
	for _, ns := range currentList {
		currentMap[ns.Name] = struct{}{}
	}

	for _, ns := range ap.Namespaces {
		if _, ok := currentMap[ns]; ok {
			delete(currentMap, ns)
			continue
		}

		obj := &api.Namespace{
			Name: ns,
		}
		if ap.IsDefault() {
			obj.ACLs = &api.NamespaceACLConfig{
				PolicyDefaults: []api.ACLLink{
					{Name: "cross-ns-catalog-read"},
				},
			}
		}

		_, _, err = nc.Create(obj, nil)
		if err != nil {
			return err
		}
		logger.Info("created namespace", "namespace", ns, "partition", ap.String())
	}

	delete(currentMap, "default")

	for ns := range currentMap {
		if _, err := nc.Delete(ns, nil); err != nil {
			return err
		}
		logger.Info("deleted namespace", "namespace", ns, "partition", ap.String())
	}

	return nil
}

func (c *Core) createReplicationToken() error {
	const replicationName = "acl-replication"

	// NOTE: this is not partition aware

	p := &api.ACLPolicy{
		Name:        replicationName,
		Description: replicationName,
	}
	if c.config.EnterpriseEnabled {
		p.Rules = `
			acl      = "write"
			operator = "write"
			service_prefix "" {
				policy     = "read"
				intentions = "read"
			}
			namespace_prefix "" {
				service_prefix "" {
					policy     = "read"
					intentions = "read"
				}
			}`
	} else {
		p.Rules = `
			acl      = "write"
			operator = "write"
			service_prefix "" {
				policy     = "read"
				intentions = "read"
			}`
	}
	p, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p, nil)
	if err != nil {
		return err
	}

	// c.logger.Info("replication policy", "name", p.Name, "id", p.ID)

	token := &api.ACLToken{
		Description: replicationName,
		Local:       false,
		Policies:    []*api.ACLTokenPolicyLink{{ID: p.ID}},
	}

	token, err = consulfunc.CreateOrUpdateToken(c.primaryClient(), token, nil)
	if err != nil {
		return err
	}
	c.setToken("replication", "", token.SecretID)

	c.logger.Info("replication token", "secretID", token.SecretID)

	return nil
}

func (c *Core) createMeshGatewayToken() error {
	const meshGatewayName = "mesh-gateway"

	p := &api.ACLPolicy{
		Name:        meshGatewayName,
		Description: meshGatewayName,
	}

	if c.config.EnterpriseEnabled {
		p.Rules = `
			namespace_prefix "" {
				service "mesh-gateway" {
					policy     = "write"
				}
				service_prefix "" {
					policy     = "read"
				}
				node_prefix "" {
					policy     = "read"
				}
			}
			agent_prefix "" {
				policy     = "read"
			}
		`
		if !c.config.EnterpriseDisablePartitions {
			p.Rules = ` partition "default" { ` + p.Rules + ` } `
		}

	} else {
		p.Rules = `
			service "mesh-gateway" {
				policy     = "write"
			}
			service_prefix "" {
				policy     = "read"
			}
			node_prefix "" {
				policy     = "read"
			}
			agent_prefix "" {
				policy     = "read"
			}
			`
	}
	p, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p, nil)
	if err != nil {
		return err
	}

	token := &api.ACLToken{
		Description: meshGatewayName,
		Local:       false,
		Policies:    []*api.ACLTokenPolicyLink{{ID: p.ID}},
	}

	token, err = consulfunc.CreateOrUpdateToken(c.primaryClient(), token, nil)
	if err != nil {
		return err
	}

	if err := c.cache.SaveValue("mesh-gateway", token.SecretID); err != nil {
		return err
	}

	// c.setToken("mesh-gateway", "", token.SecretID)

	c.logger.Info("mesh-gateway token", "secretID", token.SecretID)

	return nil
}

func (c *Core) injectReplicationToken() error {
	token := c.mustGetToken("replication", "")

	agentMasterToken := c.config.AgentMasterToken

	return c.topology.Walk(func(node *infra.Node) error {
		if node.Cluster == config.PrimaryCluster || !node.Server {
			return nil
		}

		agentClient, err := consulfunc.GetClient(node.LocalAddress(), agentMasterToken)
		if err != nil {
			return err
		}
		ac := agentClient.Agent()

	TRYAGAIN:
		_, err = ac.UpdateReplicationACLToken(token, nil)
		if err != nil {
			if strings.Index(err.Error(), "Unexpected response code: 403 (ACL not found)") != -1 {
				c.logger.Warn("system is coming up", "error", err)
				time.Sleep(250 * time.Millisecond)
				goto TRYAGAIN
			}
			return err
		}
		c.logger.Info("agent was given its replication token", "node", node.Name)

		return nil
	})
}

// each agent will get a minimal policy configured
func (c *Core) createAgentTokens() error {
	return c.topology.Walk(func(node *infra.Node) error {
		policyName := "agent--" + node.Name

		p := &api.ACLPolicy{
			Name:        policyName,
			Description: policyName,
		}

		if c.config.EnterpriseEnabled {
			p.Rules = `
			node "` + node.Name + `-pod" { policy = "write" }
			service_prefix "" { policy = "read" }
			`
			if !c.config.EnterpriseDisablePartitions {
				p.Rules = ` partition "` + node.Partition + `" { ` + p.Rules + ` } `
			}
		} else {
			p.Rules = `
			node "` + node.Name + `-pod" { policy = "write" }
			service_prefix "" { policy = "read" }
			`
		}

		_, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p, nil)
		if err != nil {
			return err
		}
		// c.logger.Info("agent policy", "name", node.Name, "id", op.ID)

		token := &api.ACLToken{
			Description: node.TokenName(),
			Local:       false,
			Policies:    []*api.ACLTokenPolicyLink{{Name: policyName}},
		}

		token, err = consulfunc.CreateOrUpdateToken(c.primaryClient(), token, nil)
		if err != nil {
			return err
		}

		c.logger.Info("agent token",
			"node", node.Name,
			"partition", node.Partition,
			"secretID", token.SecretID)

		c.setToken("agent", node.Name, token.SecretID)

		return nil
	})
}

// TALK TO EACH AGENT
func (c *Core) injectAgentTokens(datacenter string) error {
	agentMasterToken := c.config.AgentMasterToken
	return c.topology.Walk(func(node *infra.Node) error {
		if node.Cluster != datacenter {
			return nil
		}
		agentClient, err := consulfunc.GetClient(node.LocalAddress(), agentMasterToken)
		if err != nil {
			return err
		}

		ac := agentClient.Agent()

		token := c.mustGetToken("agent", node.Name)

		_, err = ac.UpdateAgentACLToken(token, nil)
		if err != nil {
			return err
		}
		c.logger.Info("agent was given its token", "node", node.Name)

		return nil
	})
}

func (c *Core) waitForLeader(cluster string) {
	client := c.clients[cluster]
	for {
		leader, err := client.Status().Leader()
		if leader != "" && err == nil {
			c.logger.Info("cluster has leader", "cluster", cluster, "leader_addr", leader)
			break
		}
		c.logger.Info("cluster has no leader yet", "cluster", cluster)
		time.Sleep(500 * time.Millisecond)
	}
}

const anonymousTokenAccessorID = "00000000-0000-0000-0000-000000000002"

func (c *Core) createAnonymousToken() error {
	if err := c.createAnonymousPolicy(); err != nil {
		return err
	}

	// TODO: should this be partition aware?

	tok := &api.ACLToken{
		AccessorID: anonymousTokenAccessorID,
		// SecretID: "anonymous",
		Description: "anonymous",
		Local:       false,
		Policies: []*api.ACLTokenPolicyLink{
			{
				Name: "anonymous",
			},
		},
	}

	_, err := consulfunc.CreateOrUpdateToken(c.primaryClient(), tok, nil)
	if err != nil {
		return err
	}

	c.logger.Info("anonymous token updated")

	return nil
}

func (c *Core) createAnonymousPolicy() error {
	p := &api.ACLPolicy{
		Name:        "anonymous",
		Description: "anonymous",
	}
	if c.config.EnterpriseEnabled {
		p.Rules = `
			namespace_prefix "" {
				node_prefix "" { policy = "read" }
				service_prefix "" { policy = "read" }
			}
			`
		if !c.config.EnterpriseDisablePartitions {
			p.Rules = ` partition_prefix "" { ` + p.Rules + ` } `
		}
	} else {
		p.Rules = `
			node_prefix "" { policy = "read" }
			service_prefix "" { policy = "read" }
			`
	}

	op, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p, nil)
	if err != nil {
		return err
	}

	c.logger.Info("anonymous policy", "name", p.Name, "id", op.ID)

	return nil
}

func (c *Core) createCrossNamespaceCatalogReadPolicy(cluster string, ap *config.Partition) error {
	if !c.config.EnterpriseEnabled {
		return nil
	}

	var (
		client = c.clientForCluster(cluster)
		logger = c.logger.With("cluster", cluster)
	)

	p := &api.ACLPolicy{
		Name:        "cross-ns-catalog-read",
		Description: "cross-ns-catalog-read",
		Rules: `
namespace_prefix "" {
  node_prefix "" { policy = "read" }
  service_prefix "" { policy = "read" }
}
`,
	}

	op, err := consulfunc.CreateOrUpdatePolicy(client, p, apiOptionsFromConfigPartition(ap, ""))
	if err != nil {
		return err
	}

	logger.Info("cross-ns-catalog-read policy", "name", p.Name, "id", op.ID, "partition", ap.String())

	return nil
}

func apiOptionsFromConfigPartition(pc *config.Partition, ns string) *consulfunc.Options {
	if pc == nil {
		return &consulfunc.Options{Namespace: ns}
	}
	return &consulfunc.Options{
		Partition: pc.Name,
		Namespace: ns,
	}
}

func (c *Core) createServiceTokens() error {
	done := make(map[util.Identifier]struct{})

	return c.topology.Walk(func(n *infra.Node) error {
		if n.Service == nil {
			return nil
		}
		if _, ok := done[n.Service.ID]; ok {
			return nil
		}

		token := &api.ACLToken{
			Description: "service--" + n.Service.ID.ID(),
			Local:       false,
			ServiceIdentities: []*api.ACLServiceIdentity{
				{
					ServiceName: n.Service.ID.Name,
				},
			},
		}
		if c.config.EnterpriseEnabled {
			token.Namespace = n.Service.ID.Namespace
			token.Partition = n.Service.ID.Partition
		}
		if c.config.EnterpriseDisablePartitions {
			token.Partition = ""
		}

		token, err := consulfunc.CreateOrUpdateToken(c.primaryClient(), token, nil)
		if err != nil {
			return err
		}

		c.logger.Info("service token created",
			"service", n.Service.ID.Name,
			"namespace", n.Service.ID.Namespace,
			"partition", n.Service.ID.Partition,
			"token", token.SecretID,
		)

		if err := c.cache.SaveValue("service-token--"+n.Service.ID.ID(), token.SecretID); err != nil {
			return err
		}

		// c.setToken("service", sn.ID(), token.SecretID)

		done[n.Service.ID] = struct{}{}
		return nil
	})
}

func (c *Core) writeCentralConfigs(cluster string) error {
	var (
		client = c.clientForCluster(cluster)
		logger = c.logger.With("cluster", cluster)
	)
	// TODO: peering (give each peer separate config entries)

	currentEntries, err := consulfunc.ListAllConfigEntries(client,
		c.config.EnterpriseEnabled,
		c.config.EnterpriseDisablePartitions)
	if err != nil {
		return err
	}

	ce := client.ConfigEntries()

	// collect upstreams and downstreams
	dm := make(map[util.Identifier]map[util.Identifier]struct{}) // dest -> src
	err = c.topology.Walk(func(n *infra.Node) error {
		if n.Service == nil {
			return nil
		}
		svc := n.Service

		src := svc.ID
		dst := svc.UpstreamID

		sm, ok := dm[dst]
		if !ok {
			sm = make(map[util.Identifier]struct{})
			dm[dst] = sm
		}

		sm[src] = struct{}{}

		return nil
	})
	if err != nil {
		return err
	}

	var stockEntries []api.ConfigEntry
	if c.config.PrometheusEnabled {
		stockEntries = append(stockEntries, &api.ProxyConfigEntry{
			Kind:      api.ProxyDefaults,
			Name:      api.ProxyConfigGlobal,
			Partition: "default",
			Config: map[string]interface{}{
				// hardcoded address of prometheus container
				"envoy_prometheus_bind_addr": "0.0.0.0:9102",
			},
		})
	}

	for dst, sm := range dm {
		entry := &api.ServiceIntentionsConfigEntry{
			Kind:      api.ServiceIntentions,
			Name:      dst.Name,
			Namespace: dst.Namespace,
			Partition: dst.Partition,
		}
		for src := range sm {
			entry.Sources = append(entry.Sources, &api.SourceIntention{
				Name:      src.Name,
				Namespace: src.Namespace,
				Partition: src.Partition,
				Action:    api.IntentionActionAllow,
			})
		}
		stockEntries = append(stockEntries, entry)
	}

	entries := c.config.ConfigEntries
	for _, stockEntry := range stockEntries {
		found := false
		for i, entry := range entries {
			if stockEntry.GetKind() != entry.GetKind() {
				continue
			}
			if stockEntry.GetName() != entry.GetName() {
				continue
			}

			switch stockEntry.GetKind() {
			case api.ProxyDefaults:
				// This one gets special case treatment.
				stockCE := stockEntry.(*api.ProxyConfigEntry)
				ce := entry.(*api.ProxyConfigEntry)
				if ce.Config == nil {
					ce.Config = make(map[string]interface{})
				}
				for k, v := range stockCE.Config {
					ce.Config[k] = v
				}
				entries[i] = ce
			case api.ServiceIntentions:
			// we deliberately do not merge these
			default:
				return fmt.Errorf("unsupported kind: %q", stockEntry.GetKind())
			}

			found = true
			break
		}
		if !found {
			entries = append(entries, stockEntry)
		}
	}

	for _, entry := range entries {
		// scrub namespace/partition from request for OSS
		if !c.config.EnterpriseEnabled {
			switch entry.GetKind() {
			case api.ProxyDefaults:
				thisEntry := entry.(*api.ProxyConfigEntry)
				thisEntry.Namespace = ""
				thisEntry.Partition = ""
			case api.ServiceIntentions:
				thisEntry := entry.(*api.ServiceIntentionsConfigEntry)
				thisEntry.Namespace = ""
				thisEntry.Partition = ""
				for _, src := range thisEntry.Sources {
					src.Namespace = ""
					src.Partition = ""
				}
			}
		}
		if c.config.EnterpriseDisablePartitions {
			switch entry.GetKind() {
			case api.ProxyDefaults:
				thisEntry := entry.(*api.ProxyConfigEntry)
				thisEntry.Partition = ""
			case api.ServiceIntentions:
				thisEntry := entry.(*api.ServiceIntentionsConfigEntry)
				thisEntry.Partition = ""
				for _, src := range thisEntry.Sources {
					src.Partition = ""
				}
			}
		}
		if _, _, err := ce.Set(entry, nil); err != nil {
			return err
		}

		ckn := consulfunc.ConfigKindName{
			Kind:      entry.GetKind(),
			Name:      entry.GetName(),
			Namespace: util.NamespaceOrDefault(entry.GetNamespace()),
			Partition: util.PartitionOrDefault(entry.GetPartition()),
		}
		delete(currentEntries, ckn)

		logger.Info("config entry created",
			"kind", entry.GetKind(),
			"name", entry.GetName(),
			"namespace", util.NamespaceOrDefault(entry.GetNamespace()),
			"partition", util.PartitionOrDefault(entry.GetPartition()),
		)
	}

	// Loop over the kinds in the order that will make the graph happy during erasure.
	for _, kind := range []string{
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
	} {
		for ckn := range currentEntries {
			if ckn.Kind != kind {
				continue
			}

			logger.Info("nuking config entry",
				"kind", ckn.Kind,
				"name", ckn.Name,
				"namespace", util.NamespaceOrDefault(ckn.Namespace),
				"partition", util.PartitionOrDefault(ckn.Partition),
			)

			var writeOpts api.WriteOptions
			if c.config.EnterpriseEnabled {
				writeOpts.Namespace = ckn.Namespace
				writeOpts.Partition = ckn.Partition
			}

			_, err = ce.Delete(ckn.Kind, ckn.Name, &writeOpts)
			if err != nil {
				return err
			}

			delete(currentEntries, ckn)
		}
	}

	return nil
}

func (c *Core) writeServiceRegistrationFiles() error {
	return c.topology.Walk(func(n *infra.Node) error {
		if n.Service == nil {
			return nil
		}

		type templateOpts struct {
			Service                     *infra.Service
			EnterpriseEnabled           bool
			EnterpriseDisablePartitions bool
			LinkWithFederation          bool
			LinkWithPeering             bool
		}
		opts := templateOpts{
			Service:                     n.Service,
			EnterpriseEnabled:           c.config.EnterpriseEnabled,
			EnterpriseDisablePartitions: c.config.EnterpriseDisablePartitions,
			LinkWithFederation:          c.topology.LinkWithFederation(),
			LinkWithPeering:             c.topology.LinkWithPeering(),
		}

		var buf bytes.Buffer
		if err := serviceRegistrationT.Execute(&buf, opts); err != nil {
			return err
		}
		regHCL := buf.String()

		filename := "servicereg__" + n.Name + "__" + n.Service.ID.Name + ".hcl"
		if err := c.cache.WriteStringFile(filename, regHCL); err != nil {
			return err
		}
		c.logger.Info("Generated service registration", "filename", filename)
		return nil
	})
}

func (c *Core) initializeKubernetes() error {
	if err := c.createAuthMethodForK8S(); err != nil {
		return err
	}
	if err := c.createBindingRulesForK8s(); err != nil {
		return err
	}

	return nil
}

const bindingRuleDescription = "devconsul--default"

func (c *Core) createBindingRulesForK8s() error {
	rule := &api.ACLBindingRule{
		AuthMethod:  "minikube",
		Description: bindingRuleDescription,
		Selector:    "",
		BindType:    api.BindingRuleBindTypeService,
		BindName:    "${serviceaccount.name}",
	}

	// TODO: conditionally sometimes put these in partitions
	orule, err := consulfunc.CreateOrUpdateBindingRule(c.primaryClient(), rule, nil)
	if err != nil {
		return err
	}

	c.logger.Info("binding rule created", "authMethod", rule.AuthMethod, "ID", orule.ID)

	return nil
}

func (c *Core) createAuthMethodForK8S() error {
	k8sHost, err := c.cache.LoadStringFile("k8s/config_host")
	if err != nil {
		return err
	}
	caCert, err := c.cache.LoadStringFile("k8s/config_ca")
	if err != nil {
		return err
	}
	jwtToken, err := c.cache.LoadStringFile("k8s/jwt_token")
	if err != nil {
		return err
	}

	// TODO: conditionally sometimes put these in partitions

	kconfig := &api.KubernetesAuthMethodConfig{
		Host:              k8sHost,
		CACert:            caCert,
		ServiceAccountJWT: jwtToken,
	}
	am := &api.ACLAuthMethod{
		Name:   "minikube",
		Type:   "kubernetes",
		Config: kconfig.RenderToConfig(),
	}

	am, err = consulfunc.CreateOrUpdateAuthMethod(c.primaryClient(), am, nil)
	if err != nil {
		return err
	}

	c.logger.Info("created auth method", "type", am.Type, "name", am.Name)

	return nil
}

func GetServiceRegistrationHCL(s infra.Service) (string, error) {
	var buf bytes.Buffer
	err := serviceRegistrationT.Execute(&buf, &s)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (c *Core) injectAgentTokensAndWaitForNodeUpdates(cluster string) error {
	var (
		client = c.clientForCluster(cluster)
		logger = c.logger.With("cluster", cluster)
	)

	if client == nil {
		return fmt.Errorf("unknown cluster: %s", cluster)
	}
	cc := client.Catalog()

	// NOTE: this is not partition aware

	for {
		if !c.config.SecurityDisableACLs {
			if err := c.injectAgentTokens(cluster); err != nil {
				return fmt.Errorf("injectAgentTokens[%s]: %v", cluster, err)
			}
		}

		// Injecting a token should bump the agent to do anti-entropy sync.
		var (
			allNodes []*api.Node
			err      error
		)
		if c.config.EnterpriseEnabled && cluster == config.PrimaryCluster {
			allNodes, err = consulfunc.ListAllNodes(client, config.PrimaryCluster,
				c.config.EnterpriseEnabled,
				c.config.EnterpriseDisablePartitions)
			if err != nil {
				return fmt.Errorf("consulfunc.ListAllNodes: %w", err)
			}

		} else {
			allNodes, _, err = cc.Nodes(nil)
			if err != nil {
				allNodes = nil
			}
		}

		stragglers := c.determineNodeUpdateStragglers(allNodes, cluster)
		if len(stragglers) == 0 {
			logger.Info("all nodes have posted node updates, so agent acl tokens are working")
			return nil
		}
		logger.Info("not all client nodes have posted node updates yet", "nodes", stragglers)

		// takes like 90s to actually right itself
		time.Sleep(5 * time.Second)
	}
}

func (c *Core) determineNodeUpdateStragglers(nodes []*api.Node, cluster string) []string {
	nm := make(map[string]*api.Node)
	for _, n := range nodes {
		nm[n.Node] = n
	}

	var out []string
	c.topology.WalkSilent(func(n *infra.Node) {
		if n.Cluster != cluster {
			return
		}

		catNode, ok := nm[n.Name+"-pod"]
		if ok && len(catNode.TaggedAddresses) > 0 {
			return
		}
		out = append(out, n.Name)
	})

	return out
}

func (c *Core) setToken(typ, k, v string) {
	if c.tokens == nil {
		c.tokens = make(map[string]string)
	}
	c.tokens[typ+"/"+k] = v
}

func (c *Core) getToken(typ, k string) string {
	if c.tokens == nil {
		return ""
	}
	return c.tokens[typ+"/"+k]
}

func (c *Core) mustGetToken(typ, k string) string {
	tok := c.getToken(typ, k)
	if tok == "" {
		panic("token for '" + typ + "/" + k + "' not set:" + jsonPretty(c.tokens))
	}
	return tok
}

func defaultValue(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
