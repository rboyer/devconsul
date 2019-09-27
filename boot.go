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

func (t *Tool) commandBoot() error {
	flag.Parse()

	var err error
	t.topology, err = InferTopology(t.config)
	if err != nil {
		return err
	}

	{
		t.clientDC1, err = consulfunc.GetClient(t.topology.LeaderIP(PrimaryDC), "" /*no token yet*/)
		if err != nil {
			return fmt.Errorf("error creating initial bootstrap client: %v", err)
		}
		consulfunc.WaitForLeader(t.logger, t.clientDC1, "dc1-server1")

		t.clientDC2, err = consulfunc.GetClient(t.topology.LeaderIP(SecondaryDC), "" /*no token yet*/)
		if err != nil {
			return fmt.Errorf("initClient: %v", err)
		}
		consulfunc.WaitForLeader(t.logger, t.clientDC2, "dc2-server1")

		t.clientDC3, err = consulfunc.GetClient(t.topology.LeaderIP(TertiaryDC), "" /*no token yet*/)
		if err != nil {
			return fmt.Errorf("initClient: %v", err)
		}
		consulfunc.WaitForLeader(t.logger, t.clientDC3, "dc3-server1")
	}

	if err := t.bootstrap(t.clientDC1); err != nil {
		return fmt.Errorf("bootstrap: %v", err)
	}

	// now we have master token set we can do anything
	t.clientDC1, err = consulfunc.GetClient(t.topology.LeaderIP(PrimaryDC), t.masterToken)
	if err != nil {
		return fmt.Errorf("initClient: %v", err)
	}
	consulfunc.WaitForUpgrade(t.logger, t.clientDC1, "dc1-server1")

	err = t.createReplicationToken()
	if err != nil {
		return fmt.Errorf("createReplicationToken: %v", err)
	}

	err = t.createMeshGatewayToken()
	if err != nil {
		return fmt.Errorf("createMeshGatewayToken: %v", err)
	}

	err = t.injectReplicationToken()
	if err != nil {
		return fmt.Errorf("injectReplicationToken: %v", err)
	}

	t.clientDC2, err = consulfunc.GetClient(t.topology.LeaderIP(SecondaryDC), t.masterToken)
	if err != nil {
		return fmt.Errorf("initClient: %v", err)
	}
	consulfunc.WaitForUpgrade(t.logger, t.clientDC2, "dc2-server1")

	t.clientDC3, err = consulfunc.GetClient(t.topology.LeaderIP(TertiaryDC), t.masterToken)
	if err != nil {
		return fmt.Errorf("initClient: %v", err)
	}
	consulfunc.WaitForUpgrade(t.logger, t.clientDC3, "dc3-server1")

	err = t.createAgentTokens()
	if err != nil {
		return fmt.Errorf("createAgentTokens: %v", err)
	}

	err = t.injectAgentTokens()
	if err != nil {
		return fmt.Errorf("injectAgentTokens: %v", err)
	}

	t.waitForNodeUpdates()

	err = t.createAnonymousToken()
	if err != nil {
		return fmt.Errorf("createAnonymousPolicy: %v", err)
	}

	err = t.writeCentralConfigs()
	if err != nil {
		return fmt.Errorf("writeCentralConfigs: %v", err)
	}

	err = t.writeServiceRegistrationFiles()
	if err != nil {
		return fmt.Errorf("writeServiceRegistrationFiles: %v", err)
	}

	if t.config.Kubernetes.Enabled {
		err = t.initializeKubernetes()
		if err != nil {
			return fmt.Errorf("initializeKubernetes: %v", err)
		}
	} else {
		err = t.createServiceTokens()
		if err != nil {
			return fmt.Errorf("createServiceTokens: %v", err)
		}
	}

	err = t.createIntentions()
	if err != nil {
		return fmt.Errorf("createIntentions: %v", err)
	}

	if err := t.cache.SaveValue("ready", "1"); err != nil {
		return err
	}

	return nil
}

func (t *Tool) bootstrap(client *api.Client) error {
	var err error
	t.masterToken, err = t.cache.LoadValue("master-token")
	if err != nil {
		return err
	}

	if t.masterToken == "" && t.config.InitialMasterToken != "" {
		t.masterToken = t.config.InitialMasterToken
		if err := t.cache.SaveValue("master-token", t.masterToken); err != nil {
			return err
		}
	}

	ac := client.ACL()

	if t.masterToken != "" {
	TRYAGAIN:
		// check to see if it works
		_, _, err := ac.TokenReadSelf(&api.QueryOptions{Token: t.masterToken})
		if err != nil {
			if strings.Index(err.Error(), "The ACL system is currently in legacy mode") != -1 {
				t.logger.Warn(fmt.Sprintf("system is rebooting: %v", err))
				time.Sleep(250 * time.Millisecond)
				goto TRYAGAIN
			}

			t.logger.Warn(fmt.Sprintf("master token doesn't work anymore: %v", err))
			return t.cache.DelValue("master-token")
		}
		t.logger.Info(fmt.Sprintf("Master Token is: %s", t.masterToken))
		return nil
	}

TRYAGAIN2:
	t.logger.Info("bootstrapping ACLs")
	tok, _, err := ac.Bootstrap()
	if err != nil {
		if strings.Index(err.Error(), "The ACL system is currently in legacy mode") != -1 {
			t.logger.Warn(fmt.Sprintf("system is rebooting: %v", err))
			time.Sleep(250 * time.Millisecond)
			goto TRYAGAIN2
		}
		return err
	}
	t.masterToken = tok.SecretID

	if err := t.cache.SaveValue("master-token", t.masterToken); err != nil {
		return err
	}

	t.logger.Info(fmt.Sprintf("Master Token is: %s", t.masterToken))

	return nil
}

func (t *Tool) createReplicationToken() error {
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
	p, err := consulfunc.CreateOrUpdatePolicy(t.clientDC1, p)
	if err != nil {
		return err
	}

	t.logger.Info(fmt.Sprintf("replication policy id for %q is: %s", p.Name, p.ID))

	token := &api.ACLToken{
		Description: replicationName,
		Local:       false,
		Policies:    []*api.ACLTokenPolicyLink{{ID: p.ID}},
	}

	token, err = consulfunc.CreateOrUpdateToken(t.clientDC1, token)
	if err != nil {
		return err
	}
	t.setToken("replication", "", token.SecretID)

	t.logger.Info(fmt.Sprintf("replication token secretID is: %s", token.SecretID))

	return nil
}

func (t *Tool) createMeshGatewayToken() error {
	const meshGatewayName = "mesh-gateway"

	token := &api.ACLToken{
		Description: meshGatewayName,
		Local:       false,
		ServiceIdentities: []*api.ACLServiceIdentity{
			{ServiceName: "mesh-gateway"},
		},
	}

	token, err := consulfunc.CreateOrUpdateToken(t.clientDC1, token)
	if err != nil {
		return err
	}

	if err := t.cache.SaveValue("mesh-gateway", token.SecretID); err != nil {
		return err
	}

	t.setToken("mesh-gateway", "", token.SecretID)

	t.logger.Info(fmt.Sprintf("mesh-gateway token secretID is: %s", token.SecretID))

	return nil
}

func (t *Tool) injectReplicationToken() error {
	token := t.mustGetToken("replication", "")

	agentMasterToken := t.runtimeConfig.AgentMasterToken

	return t.topology.Walk(func(node Node) error {
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
				t.logger.Warn(fmt.Sprintf("system is coming up: %v", err))
				time.Sleep(250 * time.Millisecond)
				goto TRYAGAIN
			}
			return err
		}
		t.logger.Info(fmt.Sprintf("[%s] agent was given its replication token", node.Name))

		return nil
	})
}

// each agent will get a minimal policy configured
func (t *Tool) createAgentTokens() error {
	return t.topology.Walk(func(node Node) error {
		policyName := "agent--" + node.Name

		p := &api.ACLPolicy{
			Name:        policyName,
			Description: policyName,
			Rules: `
node "` + node.Name + `-pod" { policy = "write" }
service_prefix "" { policy = "read" }
`,
		}

		op, err := consulfunc.CreateOrUpdatePolicy(t.clientDC1, p)
		if err != nil {
			return err
		}

		t.logger.Info(fmt.Sprintf("agent policy id for %q is: %s", node.Name, op.ID))

		token := &api.ACLToken{
			Description: node.TokenName(),
			Local:       false,
			Policies:    []*api.ACLTokenPolicyLink{{Name: policyName}},
		}

		token, err = consulfunc.CreateOrUpdateToken(t.clientDC1, token)
		if err != nil {
			return err
		}

		t.logger.Info(fmt.Sprintf("agent token secretID for %q is: %s", node.Name, token.SecretID))

		t.setToken("agent", node.Name, token.SecretID)

		return nil
	})
}

// TALK TO EACH AGENT
func (t *Tool) injectAgentTokens() error {
	return t.topology.Walk(func(node Node) error {
		agentClient, err := consulfunc.GetClient(node.LocalAddress(), t.masterToken)
		if err != nil {
			return err
		}

		consulfunc.WaitForUpgrade(t.logger, agentClient, node.Name)

		ac := agentClient.Agent()

		token := t.mustGetToken("agent", node.Name)

		_, err = ac.UpdateAgentACLToken(token, nil)
		if err != nil {
			return err
		}
		t.logger.Info(fmt.Sprintf("[%s] agent was given its token", node.Name))

		return nil
	})
}

const anonymousTokenAccessorID = "00000000-0000-0000-0000-000000000002"

func (t *Tool) createAnonymousToken() error {
	if err := t.createAnonymousPolicy(); err != nil {
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

	_, err := consulfunc.CreateOrUpdateToken(t.clientDC1, tok)
	if err != nil {
		return err
	}

	t.logger.Info("anonymous token updated")

	return nil
}

func (t *Tool) createAnonymousPolicy() error {
	p := &api.ACLPolicy{
		Name:        "anonymous",
		Description: "anonymous",
		Rules: `
node_prefix "" { policy = "read" }
service_prefix "" { policy = "read" }
`,
	}

	op, err := consulfunc.CreateOrUpdatePolicy(t.clientDC1, p)
	if err != nil {
		return err
	}

	t.logger.Info(fmt.Sprintf("anonymous policy id for %q is: %s", p.Name, op.ID))

	return nil
}

func (t *Tool) createServiceTokens() error {
	done := make(map[string]struct{})

	return t.topology.Walk(func(n Node) error {
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

		token, err := consulfunc.CreateOrUpdateToken(t.clientDC1, token)
		if err != nil {
			return err
		}

		t.logger.Info("service token created",
			"service", n.Service.Name,
			"token", token.SecretID,
		)

		if err := t.cache.SaveValue("service-token--"+n.Service.Name, token.SecretID); err != nil {
			return err
		}

		t.setToken("service", n.Service.Name, token.SecretID)

		done[n.Service.Name] = struct{}{}
		return nil
	})
}

func (t *Tool) writeCentralConfigs() error {
	// Configs live in the primary DC only.
	client := t.clientForDC(PrimaryDC)

	currentEntries, err := consulfunc.ListAllConfigEntries(client)
	if err != nil {
		return err
	}

	ce := client.ConfigEntries()

	entries := t.config.ConfigEntries
	if t.config.Monitor.Prometheus {
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

		t.logger.Info("config entry created",
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

			t.logger.Info(fmt.Sprintf("nuking config entry %s/%s", ckn.Kind, ckn.Name))

			_, err = ce.Delete(ckn.Kind, ckn.Name, nil)
			if err != nil {
				return err
			}

			delete(currentEntries, ckn)
		}
	}

	return nil
}

func (t *Tool) writeServiceRegistrationFiles() error {
	return t.topology.Walk(func(n Node) error {
		if n.Service == nil {
			return nil
		}

		var buf bytes.Buffer
		if err := serviceRegistrationT.Execute(&buf, n.Service); err != nil {
			return err
		}
		regHCL := buf.String()

		filename := "servicereg__" + n.Name + "__" + n.Service.Name + ".hcl"
		if err := t.cache.WriteStringFile(filename, regHCL); err != nil {
			return err
		}
		t.logger.Info("Generated", "filename", filename)
		return nil
	})
}

func (t *Tool) dumpIntentions(client *api.Client) (map[string]string, error) {
	cc := client.Connect()

	all, _, err := cc.Intentions(nil)
	if err != nil {
		return nil, err
	}

	m := make(map[string]string)
	for _, i := range all {
		m[intentionKey(i)] = i.ID
	}

	return m, nil
}

func intentionKey(i *api.Intention) string {
	return i.SourceName + ">" + i.DestinationName
}

func (t *Tool) createIntentions() error {
	return t.topology.Walk(func(n Node) error {
		if n.Service == nil {
			return nil
		}

		i := &api.Intention{
			SourceName:      n.Service.Name,
			DestinationName: n.Service.UpstreamName,
			Action:          api.IntentionActionAllow,
		}

		oi, err := consulfunc.CreateOrUpdateIntention(t.clientDC1, i)
		if err != nil {
			return err
		}

		t.logger.Info("created/updated intention", "src", oi.SourceName,
			"dst", oi.DestinationName, "action", oi.Action)

		return nil
	})
}

func (t *Tool) initializeKubernetes() error {
	if err := t.createAuthMethodForK8S(); err != nil {
		return err
	}
	if err := t.createBindingRulesForK8s(); err != nil {
		return err
	}

	return nil
}

const bindingRuleDescription = "devconsul--default"

func (t *Tool) createBindingRulesForK8s() error {
	rule := &api.ACLBindingRule{
		AuthMethod:  "minikube",
		Description: bindingRuleDescription,
		Selector:    "",
		BindType:    api.BindingRuleBindTypeService,
		BindName:    "${serviceaccount.name}",
	}

	orule, err := consulfunc.CreateOrUpdateBindingRule(t.clientDC1, rule)
	if err != nil {
		return err
	}

	t.logger.Info("binding rule created", "auth method", rule.AuthMethod, "ID", orule.ID)

	return nil
}

func (t *Tool) createAuthMethodForK8S() error {
	k8sHost, err := t.cache.LoadStringFile("k8s/config_host")
	if err != nil {
		return err
	}
	caCert, err := t.cache.LoadStringFile("k8s/config_ca")
	if err != nil {
		return err
	}
	jwtToken, err := t.cache.LoadStringFile("k8s/jwt_token")
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

	am, err = consulfunc.CreateOrUpdateAuthMethod(t.clientDC1, am)
	if err != nil {
		return err
	}

	t.logger.Info("created auth method", "type", am.Type, "name", am.Name)

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

func (t *Tool) waitForNodeUpdates() {
	t.waitForNodeUpdatesDC(t.clientDC1, PrimaryDC)
	t.waitForNodeUpdatesDC(t.clientDC2, SecondaryDC)
	t.waitForNodeUpdatesDC(t.clientDC3, TertiaryDC)
}

func (t *Tool) waitForNodeUpdatesDC(client *api.Client, datacenter string) {
	cc := client.Catalog()

	for {
		nodes, _, err := cc.Nodes(&api.QueryOptions{Datacenter: datacenter})
		if err != nil {
			nodes = nil
		}

		stragglers := t.determineNodeUpdateStragglers(nodes, datacenter)
		if len(stragglers) == 0 {
			t.logger.Info(fmt.Sprintf("[dc=%s] all nodes have posted node updates, so agent acl tokens are working", datacenter))
			return
		}
		t.logger.Info(fmt.Sprintf("[dc=%s] not all client nodes have posted node updates yet: %v", datacenter, stragglers))

		// takes like 90s to actually right itself
		time.Sleep(5 * time.Second)
	}
}

func (t *Tool) determineNodeUpdateStragglers(nodes []*api.Node, datacenter string) []string {
	nm := make(map[string]*api.Node)
	for _, n := range nodes {
		nm[n.Node] = n
	}

	var out []string
	t.topology.WalkSilent(func(n Node) {
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
