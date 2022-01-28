package consulfunc

import (
	"strconv"

	"github.com/hashicorp/consul/api"
)

type Options struct {
	Partition string
	Namespace string
}

func (o *Options) Read() *api.QueryOptions {
	if o == nil {
		return nil
	}
	return &api.QueryOptions{
		Partition: o.Partition,
		Namespace: o.Namespace,
	}
}

func (o *Options) Write() *api.WriteOptions {
	if o == nil {
		return nil
	}
	return &api.WriteOptions{
		Partition: o.Partition,
		Namespace: o.Namespace,
	}
}

func GetClient(ip, token string) (*api.Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = "http://" + ip + ":8500"
	cfg.Token = token
	return api.NewClient(cfg)
}

func GetTokenByDescription(client *api.Client, description string, opts *Options) (*api.ACLToken, error) {
	ac := client.ACL()
	tokens, _, err := ac.TokenList(opts.Read())
	if err != nil {
		return nil, err
	}

	for _, tokenEntry := range tokens {
		if tokenEntry.Description == description {
			token, _, err := ac.TokenRead(tokenEntry.AccessorID, opts.Read())
			if err != nil {
				return nil, err
			}

			return token, nil
		}
	}
	return nil, nil
}

func ListExistingTokenAccessorsByDescription(client *api.Client, opts *Options) (map[string]string, error) {
	ac := client.ACL()
	all, _, err := ac.TokenList(opts.Read())
	if err != nil {
		return nil, err
	}

	m := make(map[string]string)
	for _, t := range all {
		m[t.Description] = t.AccessorID
	}
	return m, nil
}

func GetPolicyByName(client *api.Client, name string, opts *Options) (*api.ACLPolicy, error) {
	ac := client.ACL()
	policies, _, err := ac.PolicyList(opts.Read())
	if err != nil {
		return nil, err
	}

	for _, policyEntry := range policies {
		if policyEntry.Name == name {
			policy, _, err := ac.PolicyRead(policyEntry.ID, opts.Read())
			if err != nil {
				return nil, err
			}

			return policy, nil
		}
	}
	return nil, nil
}

func ListExistingPoliciesByName(client *api.Client, opts *Options) (map[string]string, error) {
	ac := client.ACL()
	all, _, err := ac.PolicyList(opts.Read())
	if err != nil {
		return nil, err
	}

	m := make(map[string]string)
	for _, p := range all {
		m[p.Name] = p.ID
	}
	return m, nil
}

func ListExistingBindingRuleIDsForAuthMethod(client *api.Client, authMethod string, opts *Options) (map[string]string, error) {
	ac := client.ACL()
	all, _, err := ac.BindingRuleList(authMethod, opts.Read())
	if err != nil {
		return nil, err
	}

	m := make(map[string]string)
	for _, r := range all {
		m[r.Description] = r.ID
	}
	return m, nil
}

func HasAllNodeUpdates(nodes []*api.Node) bool {
	for _, n := range nodes {
		if len(n.TaggedAddresses) == 0 {
			return false
		}
	}
	return true
}

// unknown is "3"
func GetACLMode(client *api.Client, name string) (int, error) {
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

func CreateOrUpdateToken(client *api.Client, t *api.ACLToken, opts *Options) (*api.ACLToken, error) {
	ac := client.ACL()

	currentToken, err := GetTokenByDescription(client, t.Description, opts)
	if err != nil {
		return nil, err
	} else if currentToken != nil {
		t.AccessorID = currentToken.AccessorID
		t.SecretID = currentToken.SecretID
	}

	if t.AccessorID != "" {
		ot, _, err := ac.TokenUpdate(t, opts.Write())
		if err != nil {
			return nil, err
		}
		return ot, nil
	}

	ot, _, err := ac.TokenCreate(t, opts.Write())
	if err != nil {
		return nil, err
	}
	return ot, nil
}

func CreateOrUpdatePolicy(client *api.Client, p *api.ACLPolicy, opts *Options) (*api.ACLPolicy, error) {
	ac := client.ACL()

	currentPolicy, err := GetPolicyByName(client, p.Name, opts)
	if err != nil {
		return nil, err
	} else if currentPolicy != nil {
		p.ID = currentPolicy.ID
	}

	if p.ID != "" {
		op, _, err := ac.PolicyUpdate(p, opts.Write())
		if err != nil {
			return nil, err
		}
		return op, nil
	}

	op, _, err := ac.PolicyCreate(p, opts.Write())
	if err != nil {
		return nil, err
	}
	return op, nil
}

func CreateOrUpdateAuthMethod(client *api.Client, am *api.ACLAuthMethod, opts *Options) (*api.ACLAuthMethod, error) {
	ac := client.ACL()

	existing, _, err := ac.AuthMethodRead(am.Name, opts.Read())
	if err != nil {
		return nil, err
	}

	if existing != nil {
		om, _, err := ac.AuthMethodUpdate(am, opts.Write())
		return om, err
	}

	om, _, err := ac.AuthMethodCreate(am, opts.Write())
	return om, err
}

func CreateOrUpdateBindingRule(client *api.Client, rule *api.ACLBindingRule, opts *Options) (*api.ACLBindingRule, error) {
	ac := client.ACL()

	currentRule, err := GetBindingRuleByDescription(client, rule.Description, opts)
	if err != nil {
		return nil, err
	} else if currentRule != nil {
		rule.ID = currentRule.ID
	}

	if rule.ID != "" {
		orule, _, err := ac.BindingRuleUpdate(rule, opts.Write())
		return orule, err
	}

	orule, _, err := ac.BindingRuleCreate(rule, opts.Write())
	return orule, err
}

func GetBindingRuleByDescription(client *api.Client, description string, opts *Options) (*api.ACLBindingRule, error) {
	ac := client.ACL()

	rules, _, err := ac.BindingRuleList("", opts.Read())
	if err != nil {
		return nil, err
	}

	for _, rule := range rules {
		if rule.Description == description {
			return rule, nil
		}
	}
	return nil, nil
}
