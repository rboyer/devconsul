package main

import (
	"bytes"
	"flag"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/rboyer/devconsul/consulfunc"
)

type CommandBoot struct {
	*Core

	primaryOnly bool

	masterToken         string
	clients             map[string]*api.Client
	replicationSecretID string

	tokens map[string]string

	upgradedACLs map[string]map[string]struct{}
}

func (c *CommandBoot) Run() error {
	flag.BoolVar(&c.primaryOnly, "primary", false, "primary only")
	flag.Parse()

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

func (c *CommandBoot) waitForKV(fromDC, toDC string) {
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

func (c *CommandBoot) initPrimaryDC() error {
	var err error

	c.waitForUpgrade(PrimaryDC)

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

	err = c.createIntentions()
	if err != nil {
		return fmt.Errorf("createIntentions: %v", err)
	}

	return nil
}

func (c *CommandBoot) initSecondaryDCs() error {
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

		c.waitForKV(dc.Name, PrimaryDC)
		c.waitForKV(PrimaryDC, dc.Name)
	}

	return nil
}

func (c *CommandBoot) primaryClient() *api.Client {
	return c.clients[PrimaryDC]
}

func (c *CommandBoot) clientForDC(dc string) *api.Client {
	return c.clients[dc]
}

func (c *CommandBoot) bootstrap(client *api.Client) error {
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

func (c *CommandBoot) createReplicationToken() error {
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

func (c *CommandBoot) createMeshGatewayToken() error {
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

func (c *CommandBoot) injectReplicationToken() error {
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
func (c *CommandBoot) createAgentTokens() error {
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
func (c *CommandBoot) injectAgentTokens(datacenter string) error {
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

func (c *CommandBoot) waitForLeader(dc string) {
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

func (c *CommandBoot) waitForUpgrade(dc string) {
	c.waitForACLUpgrade(c.clients[dc], dc, dc+"-server1")
}

func (c *CommandBoot) waitForACLUpgrade(client *api.Client, dc, node string) error {
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

func (c *CommandBoot) isUpgradedACLs(dc, node string) bool {
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

func (c *CommandBoot) markUpgradedACLs(dc, node string) {
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

func (c *CommandBoot) createAnonymousToken() error {
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

func (c *CommandBoot) createAnonymousPolicy() error {
	p := &api.ACLPolicy{
		Name:        "anonymous",
		Description: "anonymous",
		Rules: `
node_prefix "" { policy = "read" }
service_prefix "" { policy = "read" }
`,
	}

	op, err := consulfunc.CreateOrUpdatePolicy(c.primaryClient(), p)
	if err != nil {
		return err
	}

	c.logger.Info("anonymous policy", "name", p.Name, "id", op.ID)

	return nil
}

func (c *CommandBoot) createServiceTokens() error {
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

		token, err := consulfunc.CreateOrUpdateToken(c.primaryClient(), token)
		if err != nil {
			return err
		}

		c.logger.Info("service token created",
			"service", n.Service.Name,
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

func (c *CommandBoot) writeCentralConfigs() error {
	// Configs live in the primary DC only.
	client := c.clientForDC(PrimaryDC)

	currentEntries, err := consulfunc.ListAllConfigEntries(client)
	if err != nil {
		return err
	}

	ce := client.ConfigEntries()

	entries := c.config.ConfigEntries
	if c.config.PrometheusEnabled {
		found := false
		for _, entry := range entries {
			if entry.GetKind() != api.ProxyDefaults {
				continue
			}
			if entry.GetName() != api.ProxyConfigGlobal {
				continue
			}
			ce := entry.(*api.ProxyConfigEntry)
			if ce.Config == nil {
				ce.Config = make(map[string]interface{})
			}
			// hardcoded address of prometheus container
			ce.Config["envoy_prometheus_bind_addr"] = "0.0.0.0:9102"
			found = true
			break
		}
		if !found {
			entries = append(entries, &api.ProxyConfigEntry{
				Kind: api.ProxyDefaults,
				Name: api.ProxyConfigGlobal,
				Config: map[string]interface{}{
					"envoy_prometheus_bind_addr": "0.0.0.0:9102",
				},
			})
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

func (c *CommandBoot) writeServiceRegistrationFiles() error {
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

func (c *CommandBoot) createIntentions() error {
	return c.topology.Walk(func(n *Node) error {
		if n.Service == nil {
			return nil
		}

		i := &api.Intention{
			SourceName:      n.Service.Name,
			DestinationName: n.Service.UpstreamName,
			Action:          api.IntentionActionAllow,
		}

		oi, err := consulfunc.CreateOrUpdateIntention(c.primaryClient(), i)
		if err != nil {
			return err
		}

		c.logger.Info("created/updated intention",
			"src", oi.SourceName,
			"dst", oi.DestinationName,
			"action", oi.Action,
		)

		return nil
	})
}

func (c *CommandBoot) initializeKubernetes() error {
	if err := c.createAuthMethodForK8S(); err != nil {
		return err
	}
	if err := c.createBindingRulesForK8s(); err != nil {
		return err
	}

	return nil
}

const bindingRuleDescription = "devconsul--default"

func (c *CommandBoot) createBindingRulesForK8s() error {
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

func (c *CommandBoot) createAuthMethodForK8S() error {
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

func (c *CommandBoot) injectAgentTokensAndWaitForNodeUpdates(datacenter string) error {
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

func (c *CommandBoot) determineNodeUpdateStragglers(nodes []*api.Node, datacenter string) []string {
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

func (c *CommandBoot) setToken(typ, k, v string) {
	if c.tokens == nil {
		c.tokens = make(map[string]string)
	}
	c.tokens[typ+"/"+k] = v
}

func (c *CommandBoot) getToken(typ, k string) string {
	if c.tokens == nil {
		return ""
	}
	return c.tokens[typ+"/"+k]
}

func (c *CommandBoot) mustGetToken(typ, k string) string {
	tok := c.getToken(typ, k)
	if tok == "" {
		panic("token for '" + typ + "/" + k + "' not set:" + jsonPretty(c.tokens))
	}
	return tok
}
