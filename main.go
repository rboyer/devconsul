package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/rboyer/safeio"
)

const (
	PrimaryDC        = "dc1"
	SecondaryDC      = "dc2"
	AgentMasterToken = "66976a62-f596-4655-8a62-78741a708c44"
)

var (
	dereg  = flag.Bool("dereg", false, "nuke the services")
	master = flag.Bool("master", false, "everybody uses master token")
)

var (
	cacheDir    string
	masterToken string
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
	os.Exit(0)
}

func run() error {
	// this needs to run from the same directory as the docker-compose file
	// for the project
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(cwd, "docker-compose.override.yml")); err != nil {
		return fmt.Errorf("this must be run from the home of the checkout: %v", err)
	}
	cacheDir = filepath.Join(cwd, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err

	}

	client, err := getClient(topo.LeaderIP(PrimaryDC), "" /*no token yet*/)
	if err != nil {
		return fmt.Errorf("error creating initial bootstrap client: %v", err)
	}

	waitForLeader(client, "dc1-server1")

	clientSecondary, err := getClient(topo.LeaderIP(SecondaryDC), "" /*no token yet*/)
	if err != nil {
		return fmt.Errorf("initClient: %v", err)
	}
	waitForLeader(clientSecondary, "dc2-server1")

	if err := bootstrap(client); err != nil {
		return fmt.Errorf("bootstrap: %v", err)
	}

	// now we have master token set we can do anything
	client, err = getClient(topo.LeaderIP(PrimaryDC), masterToken)
	if err != nil {
		return fmt.Errorf("initClient: %v", err)
	}

	waitForUpgrade(client, "dc1-server1")

	err = createAdminTokens(client)
	if err != nil {
		return fmt.Errorf("createAdminTokens: %v", err)
	}

	err = injectReplicationToken()
	if err != nil {
		return fmt.Errorf("injectReplicationToken: %v", err)
	}

	clientSecondary, err = getClient(topo.LeaderIP(SecondaryDC), masterToken)
	if err != nil {
		return fmt.Errorf("initClient: %v", err)
	}
	waitForUpgrade(clientSecondary, "dc2-server1")

	err = createAgentTokens(client)
	if err != nil {
		return fmt.Errorf("createAgentTokens: %v", err)
	}

	err = injectAgentTokens()
	if err != nil {
		return fmt.Errorf("injectAgentTokens: %v", err)
	}

	waitForNodeUpdates(client)

	err = createAnonymousToken(client)
	if err != nil {
		return fmt.Errorf("createAnonymousPolicy: %v", err)
	}

	err = createServiceTokens(client)
	if err != nil {
		return fmt.Errorf("createServiceTokens: %v", err)
	}

	err = registerServices()
	if err != nil {
		return fmt.Errorf("registerServices: %v", err)
	}

	err = createIntentions(client)
	if err != nil {
		return fmt.Errorf("createIntentions: %v", err)
	}

	return nil
}

func bootstrap(client *api.Client) error {
	var err error
	masterToken, err = loadData("master-token")
	if err != nil {
		return err
	}

	ac := client.ACL()

	if masterToken != "" {
	TRYAGAIN:
		// check to see if it works
		_, _, err = ac.TokenList(&api.QueryOptions{Token: masterToken})
		if err != nil {
			if strings.Index(err.Error(), "The ACL system is currently in legacy mode") != -1 {
				log.Printf("system is rebooting: %v", err)
				time.Sleep(250 * time.Millisecond)
				goto TRYAGAIN
			}
			log.Printf("master token doesn't work anymore: %v", err)
			return resetData()
		}
		log.Printf("Master Token is: %s", masterToken)
		return nil
	}

	log.Print("bootstrapping ACLs")
	tok, _, err := ac.Bootstrap()
	if err != nil {
		return err
	}
	masterToken = tok.SecretID
	log.Printf("Master Token is: %s", masterToken)
	return saveData("master-token", masterToken)
}

// TALK TO EACH AGENT
func registerServices() error {

	// https://www.consul.io/docs/guides/connect-production.html

	return topo.WalkServices(func(s Service) error {
		token := s.SecretID
		if token == "" {
			panic("no token")
		}
		if *master {
			token = masterToken
		}

		node := topo.Node(s.NodeName)

		mgmtClient, err := getClient(node.IPAddress, masterToken)
		if err != nil {
			return err
		}

		client, err := getClient(node.IPAddress, token)
		if err != nil {
			return err
		}
		ac := client.Agent()

		asr := s.GetRegistration()

		// nuke previous using master token
		if *dereg {
			if err := mgmtClient.Agent().ServiceDeregister(asr.Name); err != nil {
				log.Printf("WARN: force deregister of %q failed: %v", asr.Name, err)
			}
		} else {
			if !*dereg {
				if err := ac.ServiceRegister(asr); err != nil {
					return err
				}
				log.Printf("registered service %s on %s with token: %s", s.Name, node.Name, token)
			}
		}

		return nil
	})
}

func dumpIntentions(client *api.Client) (map[string]string, error) {
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

func createIntentions(client *api.Client) error {
	cc := client.Connect()

	exist, err := dumpIntentions(client)
	if err != nil {
		return err
	}

	return topo.WalkServices(func(s Service) error {
		i := s.GetIntention()

		id, ok := exist[intentionKey(i)]
		if ok {
			// update
			i.ID = id
			_, err = cc.IntentionUpdate(i, nil)
			if err != nil {
				return err
			}
		} else {
			id, _, err = cc.IntentionCreate(i, nil)
			if err != nil {
				return err
			}
		}
		log.Printf("intention for %s -> %s (allow) has id: %s", i.SourceName, i.DestinationName, id)
		return nil
	})
}

func injectReplicationToken() error {
	replicationSecretID := topo.ReadAdmin("replication-secret-id")
	if replicationSecretID == "" {
		return nil
	}
	return topo.Walk(func(node Node) error {
		if node.Datacenter != SecondaryDC || !node.Server {
			return nil
		}

		agentClient, err := getClient(node.IPAddress, AgentMasterToken)
		if err != nil {
			return err
		}
		ac := agentClient.Agent()

		_, err = ac.UpdateACLReplicationToken(replicationSecretID, nil)
		if err != nil {
			return err
		}
		log.Printf("[%s] agent was given its replication token", node.Name)

		// waitForUpgrade(agentClient, node.Name)

		return nil
	})
}

// TALK TO EACH AGENT
func injectAgentTokens() error {
	return topo.Walk(func(node Node) error {
		agentClient, err := getClient(node.IPAddress, masterToken)
		if err != nil {
			return err
		}

		waitForUpgrade(agentClient, node.Name)

		ac := agentClient.Agent()

		token := node.SecretID
		if *master {
			token = masterToken
		}

		_, err = ac.UpdateACLAgentToken(token, nil)
		if err != nil {
			return err
		}
		log.Printf("[%s] agent was given its token", node.Name)

		return nil
	})
}

// each agent will get a minimal policy configured
func createAgentTokens(client *api.Client) error {
	if err := createAgentPolicies(client); err != nil {
		return err
	}

	exist, err := listExistingTokenAccessorsByDescription(client)
	if err != nil {
		return err
	}

	return topo.Walk(func(node Node) error {
		t := node.GetACLToken()

		accessorID, ok := exist[node.TokenName()]
		if ok {
			t.AccessorID = accessorID
		}

		ot, err := createOrUpdateToken(client, t)
		if err != nil {
			return err
		}
		accessorID = ot.AccessorID
		secretID := ot.SecretID

		log.Printf("agent token secretID for %q is: %s", node.Name, secretID)

		if err := editEnvVar("AGENT_TOKEN_"+node.EnvVarName(), secretID); err != nil {
			return err
		}

		topo.UpdateNode(node.Name, func(node Node) Node {
			node.AccessorID = accessorID
			node.SecretID = secretID
			return node
		})

		return nil
	})
	return nil
}

func editEnvVar(k, v string) error {
	m, err := loadEnv()
	if err != nil {
		return err
	}

	m[k] = v

	return saveEnv(m)
}

func saveEnv(m map[string]string) error {
	var lines []string
	for k, v := range m {
		lines = append(lines, k+"="+v)
	}
	sort.Strings(lines)

	all := strings.Join(lines, "\n") + "\n"

	_, err := safeio.WriteToFile(strings.NewReader(all), ".env", 0644)
	return err
}

func loadEnv() (map[string]string, error) {
	m := make(map[string]string)

	f, err := os.Open(".env")
	if os.IsNotExist(err) {
		return m, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		parts := strings.SplitN(scan.Text(), "=", 2)
		m[parts[0]] = parts[1]
	}
	if scan.Err() != nil {
		return nil, scan.Err()
	}

	return m, nil
}

func createAgentPolicies(client *api.Client) error {
	exist, err := listExistingPoliciesByName(client)
	if err != nil {
		return err
	}

	return topo.Walk(func(node Node) error {
		p := node.GetACLPolicy()

		id, ok := exist[p.Name]
		if ok {
			p.ID = id
		}

		op, err := createOrUpdatePolicy(client, p)
		if err != nil {
			return err
		}
		id = op.ID

		log.Printf("agent policy id for %q is: %s", node.Name, id)
		return nil
	})
}

func createAdminTokens(client *api.Client) error {
	if err := createReplicationToken(client); err != nil {
		return fmt.Errorf("createReplicationToken: %v", err)
	}
	return nil
}

const (
	replicationName = "acl-replication"
)

func createReplicationToken(client *api.Client) error {
	if err := createReplicationPolicy(client); err != nil {
		return err
	}

	exist, err := listExistingTokenAccessorsByDescription(client)
	if err != nil {
		return err
	}

	t := &api.ACLToken{
		Description: replicationName,
		Local:       false,
		Policies: []*api.ACLTokenPolicyLink{
			{
				Name: replicationName,
			},
		},
	}

	accessorID, ok := exist[replicationName]
	if ok {
		t.AccessorID = accessorID
	}

	ot, err := createOrUpdateToken(client, t)
	if err != nil {
		return err
	}
	accessorID = ot.AccessorID
	secretID := ot.SecretID

	log.Printf("replication token secretID is: %s", secretID)

	if err := editEnvVar("REPLICATION_TOKEN", secretID); err != nil {
		return err
	}

	topo.SaveAdmin("replication-accessor-id", accessorID)
	topo.SaveAdmin("replication-secret-id", secretID)

	return nil
}

func createReplicationPolicy(client *api.Client) error {
	p := &api.ACLPolicy{
		Name:        replicationName,
		Description: replicationName,
		Rules:       `acl = "write"`,
	}

	exist, err := listExistingPoliciesByName(client)
	if err != nil {
		return err
	}

	id, ok := exist[p.Name]
	if ok {
		p.ID = id
	}

	op, err := createOrUpdatePolicy(client, p)
	if err != nil {
		return err
	}
	id = op.ID

	log.Printf("replication policy id for %q is: %s", p.Name, id)

	return nil
}

const anonymousTokenAccessorID = "00000000-0000-0000-0000-000000000002"

func createAnonymousToken(client *api.Client) error {
	if err := createAnonymousPolicy(client); err != nil {
		return err
	}

	t := &api.ACLToken{
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

	_, err := createOrUpdateToken(client, t)
	if err != nil {
		return err
	}

	log.Printf("anonymous token updated")

	return nil
}

func createAnonymousPolicy(client *api.Client) error {
	p := &api.ACLPolicy{
		Name:        "anonymous",
		Description: "anonymous",
		Rules: `
node_prefix "" { policy = "read" }
service_prefix "" { policy = "read" }
`,
	}

	exist, err := listExistingPoliciesByName(client)
	if err != nil {
		return err
	}

	id, ok := exist[p.Name]
	if ok {
		p.ID = id
	}

	op, err := createOrUpdatePolicy(client, p)
	if err != nil {
		return err
	}
	id = op.ID

	log.Printf("anonymous policy id for %q is: %s", p.Name, id)

	return nil
}

func createServiceTokens(client *api.Client) error {
	if err := createServicePolicies(client); err != nil {
		return err
	}

	exist, err := listExistingTokenAccessorsByDescription(client)
	if err != nil {
		return err
	}

	return topo.WalkServices(func(s Service) error {
		t := s.GetACLToken()

		accessorID, ok := exist[t.Description]
		if ok {
			t.AccessorID = accessorID
		}

		ot, err := createOrUpdateToken(client, t)
		if err != nil {
			return err
		}
		accessorID = ot.AccessorID
		secretID := ot.SecretID

		log.Printf("service token secretID for %q is: %s", t.Description, secretID)

		topo.UpdateService(s.Name, func(s Service) Service {
			s.AccessorID = accessorID
			s.SecretID = secretID
			return s
		})

		return saveData("service-token--"+s.Name, secretID)
	})
}

func createServicePolicies(client *api.Client) error {
	exist, err := listExistingPoliciesByName(client)
	if err != nil {
		return err
	}
	return topo.WalkServices(func(s Service) error {
		p := s.GetACLPolicy()

		id, ok := exist[p.Name]
		if ok {
			p.ID = id
		}

		op, err := createOrUpdatePolicy(client, p)
		if err != nil {
			return err
		}
		id = op.ID

		log.Printf("service policy id for %q is: %s", p.Name, id)
		return nil
	})
}

// ------ TOPOLOGY DEFINITION ------

type Topology struct {
	servers []string // node names
	clients []string // node names
	nm      map[string]Node

	services []string // service names
	sm       map[string]Service

	admin map[string]string
}

func (t *Topology) SaveAdmin(k, v string) {
	if t.admin == nil {
		t.admin = make(map[string]string)
	}
	t.admin[k] = v
}

func (t *Topology) ReadAdmin(k string) string {
	if t.admin == nil {
		return ""
	}
	return t.admin[k]
}

func (t *Topology) LeaderIP(datacenter string) string {
	for _, name := range t.servers {
		n := t.Node(name)
		if n.Datacenter == datacenter {
			return n.IPAddress
		}
	}
	panic("no such dc")
}

func (t *Topology) all() []string {
	o := make([]string, 0, len(t.servers)+len(t.clients))
	o = append(o, t.servers...)
	o = append(o, t.clients...)
	return o
}

func (t *Topology) AddNode(n Node) {
	if t.nm == nil {
		t.nm = make(map[string]Node)
	}

	t.nm[n.Name] = n
	if n.Server {
		t.servers = append(t.servers, n.Name)
	} else {
		t.clients = append(t.clients, n.Name)
	}
}

func (t *Topology) AddService(s Service) {
	if t.sm == nil {
		t.sm = make(map[string]Service)
	}

	t.sm[s.Name] = s
	t.services = append(t.services, s.Name)
}

func (t *Topology) UpdateNode(name string, f func(n Node) Node) {
	v := f(t.Node(name))
	if v.Name != name {
		panic("bad naming")
	}
	t.nm[name] = v
}

func (t *Topology) UpdateService(name string, f func(s Service) Service) {
	v := f(t.Service(name))
	if v.Name != name {
		panic("bad naming")
	}
	t.sm[name] = v
}

func (t *Topology) Node(name string) Node {
	if t.nm == nil {
		panic("node not found: " + name)
	}
	n, ok := t.nm[name]
	if !ok {
		panic("node not found: " + name)
	}
	return n
}

func (t *Topology) Service(name string) Service {
	if t.sm == nil {
		panic("service not found: " + name)
	}
	s, ok := t.sm[name]
	if !ok {
		panic("service not found: " + name)
	}
	return s
}

func (t *Topology) Walk(f func(n Node) error) error {
	for _, nodeName := range t.all() {
		node := t.Node(nodeName)
		if err := f(node); err != nil {
			return err
		}
	}
	return nil
}

func (t *Topology) WalkServices(f func(s Service) error) error {
	for _, serviceName := range t.services {
		s := t.Service(serviceName)
		if err := f(s); err != nil {
			return err
		}
	}
	return nil
}

type Service struct {
	Name              string
	NodeName          string
	Port              int
	UpstreamName      string
	UpstreamLocalPort int
	//
	AccessorID string
	SecretID   string
}

func (p *Service) PolicyName() string { return "service--" + p.Name }

func (p *Service) TokenName() string {
	return "service--" + p.Name + "--" + p.NodeName
}

func (p *Service) Rules() string {
	var buf bytes.Buffer
	buf.WriteString("service \"" + p.Name + "\" { policy = \"write\" }\n")
	buf.WriteString("service \"" + p.Name + "-sidecar-proxy\" { policy = \"write\" }\n")
	// // TODO: tighten the node acl
	buf.WriteString("node_prefix \"\" { policy = \"read\" }\n")
	// buf.WriteString("service \"" + p.UpstreamName + "\" { policy = \"read\" }\n")
	// buf.WriteString("service \"" + p.UpstreamName + "-sidecar-proxy\" { policy = \"read\" }")
	buf.WriteString("service_prefix \"\" { policy = \"read\" }\n")
	return buf.String()
}

func (p *Service) GetACLPolicy() *api.ACLPolicy {
	return &api.ACLPolicy{
		Name:        p.PolicyName(),
		Description: p.PolicyName(),
		Rules:       p.Rules(),
	}
}

func (p *Service) GetACLToken() *api.ACLToken {
	return &api.ACLToken{
		Description: p.TokenName(),
		Local:       false,
		Policies: []*api.ACLTokenPolicyLink{
			{
				Name: p.PolicyName(),
			},
		},
	}
}

func (s *Service) GetRegistration() *api.AgentServiceRegistration {
	return &api.AgentServiceRegistration{
		Name: s.Name,
		Port: s.Port,
		Checks: []*api.AgentServiceCheck{
			{
				CheckID:  "up",
				Name:     "up",
				HTTP:     "http://localhost:" + strconv.Itoa(s.Port) + "/healthz",
				Method:   "GET",
				Interval: "5s",
				Timeout:  "1s",
			},
		},
		Connect: &api.AgentServiceConnect{
			SidecarService: &api.AgentServiceRegistration{
				Proxy: &api.AgentServiceConnectProxyConfig{
					Upstreams: []api.Upstream{
						{
							DestinationName: s.UpstreamName,
							LocalBindPort:   s.UpstreamLocalPort,
						},
					},
				},
			},
		},
	}
}

func (s *Service) GetIntention() *api.Intention {
	return &api.Intention{
		SourceName:      s.Name,
		DestinationName: s.UpstreamName,
		Action:          api.IntentionActionAllow,
	}
}

type Node struct {
	Datacenter string
	Name       string
	Server     bool
	IPAddress  string
	Services   []string
	//
	AccessorID string
	SecretID   string
}

func (n *Node) PolicyName() string { return "agent--" + n.Name }
func (n *Node) TokenName() string  { return "agent--" + n.Name }
func (n *Node) Rules() string      { return `node "` + n.Name + `-pod" { policy = "write" } ` }

func (n *Node) EnvVarName() string {
	return strings.ToUpper(strings.Replace(n.Name, "-", "_", -1))
}

func (n *Node) GetACLPolicy() *api.ACLPolicy {
	return &api.ACLPolicy{
		Name:        n.PolicyName(),
		Description: n.PolicyName(),
		Rules:       n.Rules(),
	}
}

func (n *Node) GetACLToken() *api.ACLToken {
	return &api.ACLToken{
		Description: n.TokenName(),
		Local:       false,
		Policies: []*api.ACLTokenPolicyLink{
			{
				Name: n.PolicyName(),
			},
		},
	}
}

var topo Topology

func init() {
	topo.AddNode(Node{
		Datacenter: "dc1",
		Name:       "dc1-server1",
		Server:     true,
		IPAddress:  "10.0.1.11",
		Services:   nil,
	})
	topo.AddNode(Node{
		Datacenter: "dc2",
		Name:       "dc2-server1",
		Server:     true,
		IPAddress:  "10.0.2.11",
		Services:   nil,
	})
	topo.AddNode(Node{
		Datacenter: "dc1",
		Name:       "dc1-client1",
		IPAddress:  "10.0.1.12",
		Services:   []string{"ping"},
	})
	topo.AddNode(Node{
		Datacenter: "dc1",
		Name:       "dc1-client2",
		IPAddress:  "10.0.1.13",
		Services:   []string{"pong"},
	})

	topo.AddService(Service{
		Name:              "ping",
		NodeName:          "dc1-client1",
		Port:              8080,
		UpstreamName:      "pong",
		UpstreamLocalPort: 9090,
	})
	topo.AddService(Service{
		Name:              "pong",
		NodeName:          "dc1-client2",
		Port:              8080,
		UpstreamName:      "ping",
		UpstreamLocalPort: 9090,
	})
}

// ------ UTILITY FUNCTIONS ------

func listExistingTokenAccessorsByDescription(client *api.Client) (map[string]string, error) {
	ac := client.ACL()
	all, _, err := ac.TokenList(nil)
	if err != nil {
		return nil, err
	}

	m := make(map[string]string)
	for _, t := range all {
		m[t.Description] = t.AccessorID
	}
	return m, nil
}

func createOrUpdateToken(client *api.Client, t *api.ACLToken) (*api.ACLToken, error) {
	ac := client.ACL()

	if t.AccessorID != "" {
		ot, _, err := ac.TokenUpdate(t, nil)
		if err != nil {
			return nil, err
		}
		return ot, nil
	}

	ot, _, err := ac.TokenCreate(t, nil)
	if err != nil {
		return nil, err
	}
	return ot, nil
}

func listExistingPoliciesByName(client *api.Client) (map[string]string, error) {
	ac := client.ACL()
	all, _, err := ac.PolicyList(nil)
	if err != nil {
		return nil, err
	}

	m := make(map[string]string)
	for _, p := range all {
		m[p.Name] = p.ID
	}
	return m, nil
}

// manually do this part
func getClient(ip, token string) (*api.Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = "http://" + ip + ":8500"
	cfg.Token = token
	return api.NewClient(cfg)
}

func createOrUpdatePolicy(client *api.Client, p *api.ACLPolicy) (*api.ACLPolicy, error) {
	ac := client.ACL()

	if p.ID != "" {
		op, _, err := ac.PolicyUpdate(p, nil)
		if err != nil {
			return nil, err
		}
		return op, nil
	}

	op, _, err := ac.PolicyCreate(p, nil)
	if err != nil {
		return nil, err
	}
	return op, nil
}

func waitForLeader(client *api.Client, name string) {
	sc := client.Status()

	for {
		leader, err := sc.Leader()
		if leader != "" && err == nil {
			log.Printf("[%s] leader is %q", name, leader)
			return
		}
		log.Printf("[%s] no leader yet", name)
		time.Sleep(500 * time.Millisecond)
	}
}

func waitForUpgrade(client *api.Client, name string) {
	for {
		// map[string]map[string]interface{}
		mode, err := getSelfACLMode(client, name)
		if err == nil && mode == 1 {
			log.Printf("[%s] acl mode is now in v2 mode", name)
			return
		}
		log.Printf("[%s] acl mode not upgraded to v2 yet", name)

		time.Sleep(500 * time.Millisecond)
	}
}

func waitForNodeUpdates(client *api.Client) {
	waitForNodeUpdatesDC(client, PrimaryDC)
	waitForNodeUpdatesDC(client, SecondaryDC)
}
func waitForNodeUpdatesDC(client *api.Client, datacenter string) {
	cc := client.Catalog()

	for {
		nodes, _, err := cc.Nodes(&api.QueryOptions{Datacenter: datacenter})
		if err != nil {
			nodes = nil
		}

		stragglers := determineNodeUpdateStragglers(nodes, datacenter)
		if len(stragglers) == 0 {
			log.Printf("[dc=%s] all nodes have posted node updates, so agent acl tokens are working", datacenter)
			return
		}
		log.Printf("[dc=%s] not all client nodes have posted node updates yet: %v", datacenter, stragglers)

		// takes like 90s to actually right itself
		time.Sleep(5 * time.Second)
	}
}

func determineNodeUpdateStragglers(nodes []*api.Node, datacenter string) []string {
	nm := make(map[string]*api.Node)
	for _, n := range nodes {
		nm[n.Node] = n
	}

	var out []string
	for _, nodeName := range topo.all() {
		n := topo.Node(nodeName)
		if n.Datacenter != datacenter {
			continue
		}

		catNode, ok := nm[n.Name+"-pod"]
		if ok && len(catNode.TaggedAddresses) > 0 {
			continue
		}
		out = append(out, n.Name)
	}
	return out
}

func hasAllNodeUpdates(nodes []*api.Node) bool {
	for _, n := range nodes {
		if len(n.TaggedAddresses) == 0 {
			return false
		}
	}
	return true
}

// unknown is "3"
func getSelfACLMode(client *api.Client, name string) (int, error) {
	ac := client.Agent()

	// map[string]map[string]interface{}
	info, err := ac.Self()
	if err != nil {
		return 3, err
	}
	m, ok := info["Member"]
	if !ok {
		return 3, nil
	}
	t, ok := m["Tags"]
	if !ok {
		return 3, nil
	}
	tm, ok := t.(map[string]interface{})
	if !ok {
		return 3, nil
	}
	acls, ok := tm["acls"]
	if !ok {
		return 3, nil
	}
	a, ok := acls.(string)
	if !ok {
		return 3, nil
	}

	v, err := strconv.Atoi(a)
	if err != nil {
		return 3, err
	}
	return v, nil
}

func loadData(name string) (string, error) {
	fn := filepath.Join(cacheDir, name+".val")
	b, err := ioutil.ReadFile(fn)
	if os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func saveData(name, value string) error {
	fn := filepath.Join(cacheDir, name+".val")
	_, err := safeio.WriteToFile(strings.NewReader(value), fn, 0644)
	return err
}

func resetData() error {
	items, err := ioutil.ReadDir(cacheDir)
	if err != nil {
		return err
	}
	for _, item := range items {
		fn := filepath.Join(cacheDir, item.Name())
		if item.IsDir() {
			return fmt.Errorf("please manually erase whatever garbage this is: %s", fn)
		}
		log.Printf("nuking stale: %s", fn)
		if err := os.Remove(fn); err != nil {
			return err
		}
	}
	return errors.New("please restart the tool; the data was reset")
}
