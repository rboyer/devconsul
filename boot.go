package main

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/rboyer/devconsul/consulfunc"
)

type BootInfo struct {
	primaryOnly bool

	masterToken         string
	clients             map[string]*api.Client
	replicationSecretID string

	tokens map[string]string

	upgradedACLs map[string]map[string]struct{}
}

func (c *Core) runBoot(primaryOnly bool) error {
	if err := checkHasRunOnce("init"); err != nil {
		return err
	}

	c.primaryOnly = primaryOnly

	if c.primaryOnly {
		c.logger.Info("only bootstrapping the primary datacenter", "dc", PrimaryDC)
	}

	var err error

	c.clients = make(map[string]*api.Client)
	for _, dc := range c.topology.Datacenters() {
		if c.primaryOnly && !dc.Primary {
			continue
		}
		c.clients[dc.Name], err = consulfunc.GetClient(c.topology.LeaderIP(dc.Name, false), "" /*no token yet*/)
		if err != nil {
			return fmt.Errorf("error creating initial bootstrap client for dc=%s: %v", dc.Name, err)
		}

		c.waitForLeader(dc.Name)
	}

	if err := c.bootstrap(c.primaryClient()); err != nil {
		return fmt.Errorf("bootstrap: %v", err)
	}

	// now we have master token set we can do anything
	c.clients[PrimaryDC], err = consulfunc.GetClient(c.topology.LeaderIP(PrimaryDC, false), c.masterToken)
	if err != nil {
		return fmt.Errorf("error creating final client for dc=%s: %v", PrimaryDC, err)
	}

	if err := c.initPrimaryDC(); err != nil {
		return err
	}

	if !c.primaryOnly {
		if err := c.initSecondaryDCs(); err != nil {
			return err
		}
	}

	if err := c.cache.SaveValue("ready", "1"); err != nil {
		return err
	}

	return nil
}

func (c *Core) waitForKV(fromDC, toDC string) {
	client := c.clients[fromDC]

	for {
		_, err := client.KV().Put(&api.KVPair{
			Key:   "test-from-" + fromDC + "-to-" + toDC,
			Value: []byte("payload-for-" + fromDC + "-to-" + toDC),
		}, &api.WriteOptions{
			Datacenter: toDC,
		})

		if err == nil {
			c.logger.Info("kv write success",
				"from_dc", fromDC, "to_dc", toDC,
			)
			return
		}

		c.logger.Warn("kv write failed; wan not converged yet",
			"from_dc", fromDC, "to_dc", toDC,
		)
		time.Sleep(500 * time.Millisecond)
	}
}

func (c *Core) initPrimaryDC() error {
	var err error

	c.waitForUpgrade(PrimaryDC)

	err = c.createNamespaces()
	if err != nil {
		return fmt.Errorf("createNamespaces: %v", err)
	}

	err = c.createReplicationToken()
	if err != nil {
		return fmt.Errorf("createReplicationToken: %v", err)
	}

	err = c.createMeshGatewayToken()
	if err != nil {
		return fmt.Errorf("createMeshGatewayToken: %v", err)
	}

	err = c.createAgentTokens()
	if err != nil {
		return fmt.Errorf("createAgentTokens: %v", err)
	}

	err = c.injectAgentTokensAndWaitForNodeUpdates(PrimaryDC)
	if err != nil {
		return fmt.Errorf("injectAgentTokensAndWaitForNodeUpdates[%s]: %v", PrimaryDC, err)
	}

	err = c.createAnonymousToken()
	if err != nil {
		return fmt.Errorf("createAnonymousPolicy: %v", err)
	}

	err = c.writeCentralConfigs()
	if err != nil {
		return fmt.Errorf("writeCentralConfigs: %v", err)
	}

	err = c.writeServiceRegistrationFiles()
	if err != nil {
		return fmt.Errorf("writeServiceRegistrationFiles: %v", err)
	}

	if c.config.KubernetesEnabled {
		err = c.initializeKubernetes()
		if err != nil {
			return fmt.Errorf("initializeKubernetes: %v", err)
		}
	} else {
		err = c.createServiceTokens()
		if err != nil {
			return fmt.Errorf("createServiceTokens: %v", err)
		}
	}

	return nil
}

func (c *Core) initSecondaryDCs() error {
	var err error

	err = c.injectReplicationToken()
	if err != nil {
		return fmt.Errorf("injectReplicationToken: %v", err)
	}

	for _, dc := range c.topology.Datacenters() {
		if dc.Primary {
			continue
		}
		c.clients[dc.Name], err = consulfunc.GetClient(c.topology.LeaderIP(dc.Name, false), c.masterToken)
		if err != nil {
			return fmt.Errorf("error creating final client for dc=%s: %v", dc.Name, err)
		}
		c.waitForUpgrade(dc.Name)

		err = c.injectAgentTokensAndWaitForNodeUpdates(dc.Name)
		if err != nil {
			return fmt.Errorf("injectAgentTokensAndWaitForNodeUpdates[%s]: %v", dc.Name, err)
		}
	}

	for _, dc1 := range c.topology.Datacenters() {
		for _, dc2 := range c.topology.Datacenters() {
			if dc1 == dc2 {
				continue
			}
			c.waitForKV(dc1.Name, dc2.Name)
		}
	}

	return nil
}

func (c *Core) primaryClient() *api.Client {
	return c.clients[PrimaryDC]
}

func (c *Core) clientForDC(dc string) *api.Client {
	return c.clients[dc]
}

func (c *Core) bootstrap(client *api.Client) error {
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

func (c *Core) createNamespaces() error {
	if !c.config.EnterpriseEnabled {
		return nil
	}

	// Create a policy to allow super permissive catalog reads across namespace
	// boundaries.
	if err := c.createCrossNamespaceCatalogReadPolicy(); err != nil {
		return fmt.Errorf("createCrossNamespaceCatalogReadPolicy(): %v", err)
	}

	nc := c.primaryClient().Namespaces()

	currentList, _, err := nc.List(nil)
	if err != nil {
		return err
	}

	currentMap := make(map[string]struct{})
	for _, ns := range currentList {
		currentMap[ns.Name] = struct{}{}
	}

	for _, ns := range c.config.EnterpriseNamespaces {
		if _, ok := currentMap[ns]; ok {
			delete(currentMap, ns)
			continue
		}

		obj := &api.Namespace{
			Name: ns,
			ACLs: &api.NamespaceACLConfig{
				PolicyDefaults: []api.ACLLink{
					{Name: "cross-ns-catalog-read"},
				},
			},
		}

		_, _, err = nc.Create(obj, nil)
		if err != nil {
			return err
		}
		c.logger.Info("created namespace", "namespace", ns)
	}

	delete(currentMap, "default")

	for ns, _ := range currentMap {
		if _, err := nc.Delete(ns, nil); err != nil {
			return err
		}
		c.logger.Info("deleted namespace", "namespace", ns)
	}

	return nil
}

func (c *Core) createReplicationToken() error {
	const replicationName = "acl-replication"

	p := &api.ACLPolicy{
		Name:        replicationName,
		Description: replicationName,
		Rules: `
acl      = "write"
operator = "write"
service_prefix "" {
	policy     = "read"
	intentions = "read"
}`,
	}
	p, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p)
	if err != nil {
		return err
	}

	// c.logger.Info("replication policy", "name", p.Name, "id", p.ID)

	token := &api.ACLToken{
		Description: replicationName,
		Local:       false,
		Policies:    []*api.ACLTokenPolicyLink{{ID: p.ID}},
	}

	token, err = consulfunc.CreateOrUpdateToken(c.primaryClient(), token)
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
		Rules: `
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
`,
	}
	p, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p)
	if err != nil {
		return err
	}

	token := &api.ACLToken{
		Description: meshGatewayName,
		Local:       false,
		// ServiceIdentities: []*api.ACLServiceIdentity{
		// 	{ServiceName: "mesh-gateway"},
		// },
		Policies: []*api.ACLTokenPolicyLink{{ID: p.ID}},
	}

	token, err = consulfunc.CreateOrUpdateToken(c.primaryClient(), token)
	if err != nil {
		return err
	}

	if err := c.cache.SaveValue("mesh-gateway", token.SecretID); err != nil {
		return err
	}

	c.setToken("mesh-gateway", "", token.SecretID)

	c.logger.Info("mesh-gateway token", "secretID", token.SecretID)

	return nil
}

func (c *Core) injectReplicationToken() error {
	token := c.mustGetToken("replication", "")

	agentMasterToken := c.config.AgentMasterToken

	return c.topology.Walk(func(node *Node) error {
		if node.Datacenter == PrimaryDC || !node.Server {
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
	return c.topology.Walk(func(node *Node) error {
		policyName := "agent--" + node.Name

		p := &api.ACLPolicy{
			Name:        policyName,
			Description: policyName,
			Rules: `
node "` + node.Name + `-pod" { policy = "write" }
service_prefix "" { policy = "read" }
`,
		}

		_, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p)
		if err != nil {
			return err
		}
		// c.logger.Info("agent policy", "name", node.Name, "id", op.ID)

		token := &api.ACLToken{
			Description: node.TokenName(),
			Local:       false,
			Policies:    []*api.ACLTokenPolicyLink{{Name: policyName}},
		}

		token, err = consulfunc.CreateOrUpdateToken(c.primaryClient(), token)
		if err != nil {
			return err
		}

		c.logger.Info("agent token", "node", node.Name, "secretID", token.SecretID)

		c.setToken("agent", node.Name, token.SecretID)

		return nil
	})
}

// TALK TO EACH AGENT
func (c *Core) injectAgentTokens(datacenter string) error {
	agentMasterToken := c.config.AgentMasterToken
	return c.topology.Walk(func(node *Node) error {
		if node.Datacenter != datacenter {
			return nil
		}
		agentClient, err := consulfunc.GetClient(node.LocalAddress(), agentMasterToken)
		if err != nil {
			return err
		}

		// c.waitForACLUpgrade(agentClient, node.Datacenter, node.Name)

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

func (c *Core) waitForLeader(dc string) {
	client := c.clients[dc]
	for {
		leader, err := client.Status().Leader()
		if leader != "" && err == nil {
			c.logger.Info("datacenter has leader", "datacenter", dc, "leader_addr", leader)
			break
		}
		c.logger.Info("datacenter has no leader yet", "datacenter", dc)
		time.Sleep(500 * time.Millisecond)
	}
}

func (c *Core) waitForUpgrade(dc string) {
	c.waitForACLUpgrade(c.clients[dc], dc, dc+"-server1")
}

func (c *Core) waitForACLUpgrade(client *api.Client, dc, node string) error {
	if c.isUpgradedACLs(dc, node) {
		return nil
	}

	for {
		mode, err := consulfunc.GetACLMode(client, node)
		if err == nil && mode == 1 {
			c.logger.Info("acl mode is now in v2 mode", "node", node)
			break
		}
		c.logger.Info("acl mode not upgraded to v2 yet", "node", node)

		time.Sleep(500 * time.Millisecond)
	}

	c.markUpgradedACLs(dc, node)
	return nil
}

func (c *Core) isUpgradedACLs(dc, node string) bool {
	if len(c.upgradedACLs) == 0 {
		return false
	}
	m, ok := c.upgradedACLs[dc]
	if !ok {
		return false
	}
	_, ok = m[node]
	return ok
}

func (c *Core) markUpgradedACLs(dc, node string) {
	if c.upgradedACLs == nil {
		c.upgradedACLs = make(map[string]map[string]struct{})
	}
	m, ok := c.upgradedACLs[dc]
	if !ok {
		m = make(map[string]struct{})
		c.upgradedACLs[dc] = m
	}
	m[node] = struct{}{}
}

const anonymousTokenAccessorID = "00000000-0000-0000-0000-000000000002"

func (c *Core) createAnonymousToken() error {
	if err := c.createAnonymousPolicy(); err != nil {
		return err
	}

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

	_, err := consulfunc.CreateOrUpdateToken(c.primaryClient(), tok)
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
		Rules: `
node_prefix "" { policy = "read" }
service_prefix "" { policy = "read" }
`,
	}
	if c.config.EnterpriseEnabled {
		p.Rules = `namespace_prefix "" { ` + p.Rules + ` }`
	}

	op, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p)
	if err != nil {
		return err
	}

	c.logger.Info("anonymous policy", "name", p.Name, "id", op.ID)

	return nil
}

func (c *Core) createCrossNamespaceCatalogReadPolicy() error {
	if !c.config.EnterpriseEnabled {
		return nil
	}

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

	op, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p)
	if err != nil {
		return err
	}

	c.logger.Info("cross-ns-catalog-read policy", "name", p.Name, "id", op.ID)

	return nil
}

func (c *Core) createServiceTokens() error {
	done := make(map[string]struct{})

	return c.topology.Walk(func(n *Node) error {
		if n.Service == nil {
			return nil
		}
		if _, ok := done[n.Service.Name]; ok {
			return nil
		}

		token := &api.ACLToken{
			Description: "service--" + n.Service.Name,
			Local:       false,
			ServiceIdentities: []*api.ACLServiceIdentity{
				&api.ACLServiceIdentity{
					ServiceName: n.Service.Name,
				},
			},
		}
		if n.Service.Namespace != "" {
			token.Namespace = n.Service.Namespace
		}

		token, err := consulfunc.CreateOrUpdateToken(c.primaryClient(), token)
		if err != nil {
			return err
		}

		c.logger.Info("service token created",
			"service", n.Service.Name,
			"namespace", n.Service.Namespace,
			"token", token.SecretID,
		)

		if err := c.cache.SaveValue("service-token--"+n.Service.Name, token.SecretID); err != nil {
			return err
		}

		c.setToken("service", n.Service.Name, token.SecretID)

		done[n.Service.Name] = struct{}{}
		return nil
	})
}

func (c *Core) writeCentralConfigs() error {
	// Configs live in the primary DC only.
	client := c.clientForDC(PrimaryDC)

	currentEntries, err := consulfunc.ListAllConfigEntries(client)
	if err != nil {
		return err
	}

	ce := client.ConfigEntries()

	type ServiceName struct {
		Name      string
		Namespace string
	}

	// collect upstreams and downstreams
	dm := make(map[ServiceName]map[ServiceName]struct{}) // dest -> src
	err = c.topology.Walk(func(n *Node) error {
		if n.Service == nil {
			return nil
		}
		svc := n.Service

		dst := ServiceName{
			Name:      svc.Name,
			Namespace: defaultValue(svc.Namespace, "default"),
		}
		src := ServiceName{
			Name:      svc.UpstreamName,
			Namespace: defaultValue(svc.UpstreamNamespace, "default"),
		}
		if !c.config.EnterpriseEnabled {
			dst.Namespace = ""
			src.Namespace = ""
		}

		sm, ok := dm[dst]
		if !ok {
			sm = make(map[ServiceName]struct{})
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
			Kind: api.ProxyDefaults,
			Name: api.ProxyConfigGlobal,
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
		}
		for src, _ := range sm {
			entry.Sources = append(entry.Sources, &api.SourceIntention{
				Name:      src.Name,
				Namespace: src.Namespace,
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
		if _, _, err := ce.Set(entry, nil); err != nil {
			return err
		}

		ckn := consulfunc.ConfigKindName{
			Kind: entry.GetKind(),
			Name: entry.GetName(),
		}
		delete(currentEntries, ckn)

		c.logger.Info("config entry created",
			"kind", entry.GetKind(),
			"name", entry.GetName(),
		)
	}

	// Loop over the kinds in the order that will make the graph happy during erasure.
	for _, kind := range []string{
		api.ServiceIntentions,
		api.IngressGateway,
		api.TerminatingGateway,
		api.ServiceRouter,
		api.ServiceSplitter,
		api.ServiceResolver,
		api.ServiceDefaults,
		api.ProxyDefaults,
	} {
		for ckn, _ := range currentEntries {
			if ckn.Kind != kind {
				continue
			}

			c.logger.Info("nuking config entry",
				"kind", ckn.Kind,
				"name", ckn.Name,
			)

			_, err = ce.Delete(ckn.Kind, ckn.Name, nil)
			if err != nil {
				return err
			}

			delete(currentEntries, ckn)
		}
	}

	return nil
}

func (c *Core) writeServiceRegistrationFiles() error {
	return c.topology.Walk(func(n *Node) error {
		if n.Service == nil {
			return nil
		}

		var buf bytes.Buffer
		if err := serviceRegistrationT.Execute(&buf, n.Service); err != nil {
			return err
		}
		regHCL := buf.String()

		filename := "servicereg__" + n.Name + "__" + n.Service.Name + ".hcl"
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

	orule, err := consulfunc.CreateOrUpdateBindingRule(c.primaryClient(), rule)
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

	am, err = consulfunc.CreateOrUpdateAuthMethod(c.primaryClient(), am)
	if err != nil {
		return err
	}

	c.logger.Info("created auth method", "type", am.Type, "name", am.Name)

	return nil
}

var serviceRegistrationT = template.Must(template.New("service_reg").Parse(`
services = [
  {
    name = "{{.Name}}"
{{- if .Namespace }}
    namespace = "{{.Namespace}}"
{{- end }}
    port = {{.Port}}

    checks = [
      {
        name     = "up"
        http     = "http://localhost:{{.Port}}/healthz"
        method   = "GET"
        interval = "5s"
        timeout  = "1s"
      },
    ]

    meta {
{{- range $k, $v := .Meta }}
      "{{ $k }}" = "{{ $v }}",
{{- end }}
    }

    connect {
      sidecar_service {
        proxy {
          upstreams = [
            {
              destination_name = "{{.UpstreamName}}"
{{- if .UpstreamNamespace }}
              destination_namespace = "{{.UpstreamNamespace}}"
{{- end }}
              local_bind_port  = {{.UpstreamLocalPort}}
{{- if .UpstreamDatacenter }}
              datacenter = "{{.UpstreamDatacenter}}"
{{- end }}
{{ .UpstreamExtraHCL }}
            },
          ]
        }
      }
    }
  },
]
`))

func GetServiceRegistrationHCL(s Service) (string, error) {
	var buf bytes.Buffer
	err := serviceRegistrationT.Execute(&buf, &s)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (c *Core) injectAgentTokensAndWaitForNodeUpdates(datacenter string) error {
	client := c.clientForDC(datacenter)
	if client == nil {
		return fmt.Errorf("unknown datacenter: %s", datacenter)
	}
	cc := client.Catalog()

	for {
		if err := c.injectAgentTokens(datacenter); err != nil {
			return fmt.Errorf("injectAgentTokens[%s]: %v", datacenter, err)
		}

		// Injecting a token should bump the agent to do anti-entropy sync.
		nodes, _, err := cc.Nodes(&api.QueryOptions{Datacenter: datacenter})
		if err != nil {
			nodes = nil
		}

		stragglers := c.determineNodeUpdateStragglers(nodes, datacenter)
		if len(stragglers) == 0 {
			c.logger.Info("all nodes have posted node updates, so agent acl tokens are working", "datacenter", datacenter)
			return nil
		}
		c.logger.Info("not all client nodes have posted node updates yet", "datacenter", datacenter, "nodes", stragglers)

		// takes like 90s to actually right itself
		time.Sleep(5 * time.Second)
	}
}

func (c *Core) determineNodeUpdateStragglers(nodes []*api.Node, datacenter string) []string {
	nm := make(map[string]*api.Node)
	for _, n := range nodes {
		nm[n.Node] = n
	}

	var out []string
	c.topology.WalkSilent(func(n *Node) {
		if n.Datacenter != datacenter {
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
