package consulfunc

import (
	"strconv"

	"github.com/hashicorp/consul/api"
)

func GetClient(ip, token string) (*api.Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = "http://" + ip + ":8500"
	cfg.Token = token
	return api.NewClient(cfg)
}

func GetTokenByDescription(client *api.Client, description string) (*api.ACLToken, error) {
	ac := client.ACL()
	tokens, _, err := ac.TokenList(nil)
	if err != nil {
		return nil, err
	}

	for _, tokenEntry := range tokens {
		if tokenEntry.Description == description {
			token, _, err := ac.TokenRead(tokenEntry.AccessorID, nil)
			if err != nil {
				return nil, err
			}

			return token, nil
		}
	}
	return nil, nil
}

func ListExistingTokenAccessorsByDescription(client *api.Client) (map[string]string, error) {
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

func GetPolicyByName(client *api.Client, name string) (*api.ACLPolicy, error) {
	ac := client.ACL()
	policies, _, err := ac.PolicyList(nil)
	if err != nil {
		return nil, err
	}

	for _, policyEntry := range policies {
		if policyEntry.Name == name {
			policy, _, err := ac.PolicyRead(policyEntry.ID, nil)
			if err != nil {
				return nil, err
			}

			return policy, nil
		}
	}
	return nil, nil
}

func ListExistingPoliciesByName(client *api.Client) (map[string]string, error) {
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

func ListExistingBindingRuleIDsForAuthMethod(client *api.Client, authMethod string) (map[string]string, error) {
	ac := client.ACL()
	all, _, err := ac.BindingRuleList(authMethod, nil)
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

func CreateOrUpdateToken(client *api.Client, t *api.ACLToken) (*api.ACLToken, error) {
	ac := client.ACL()

	currentToken, err := GetTokenByDescription(client, t.Description)
	if err != nil {
		return nil, err
	} else if currentToken != nil {
		t.AccessorID = currentToken.AccessorID
		t.SecretID = currentToken.SecretID
	}

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

func CreateOrUpdatePolicy(client *api.Client, p *api.ACLPolicy) (*api.ACLPolicy, error) {
	ac := client.ACL()

	currentPolicy, err := GetPolicyByName(client, p.Name)
	if err != nil {
		return nil, err
	} else if currentPolicy != nil {
		p.ID = currentPolicy.ID
	}

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

func CreateOrUpdateAuthMethod(client *api.Client, am *api.ACLAuthMethod) (*api.ACLAuthMethod, error) {
	ac := client.ACL()

	existing, _, err := ac.AuthMethodRead(am.Name, nil)
	if err != nil {
		return nil, err
	}

	if existing != nil {
		om, _, err := ac.AuthMethodUpdate(am, nil)
		return om, err
	}

	om, _, err := ac.AuthMethodCreate(am, nil)
	return om, err
}

func CreateOrUpdateBindingRule(client *api.Client, rule *api.ACLBindingRule) (*api.ACLBindingRule, error) {
	ac := client.ACL()

	currentRule, err := GetBindingRuleByDescription(client, rule.Description)
	if err != nil {
		return nil, err
	} else if currentRule != nil {
		rule.ID = currentRule.ID
	}

	if rule.ID != "" {
		orule, _, err := ac.BindingRuleUpdate(rule, nil)
		return orule, err
	}

	orule, _, err := ac.BindingRuleCreate(rule, nil)
	return orule, err
}

func GetBindingRuleByDescription(client *api.Client, description string) (*api.ACLBindingRule, error) {
	ac := client.ACL()

	rules, _, err := ac.BindingRuleList("", nil)
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

func CreateOrUpdateIntention(client *api.Client, i *api.Intention) (*api.Intention, error) {
	cc := client.Connect()

	key := intentionKey(i)

	currentIntention, err := getIntentionByKey(client, key)
	if err != nil {
		return nil, err
	} else if currentIntention != nil {
		i.ID = currentIntention.ID
	}

	if i.ID != "" {
		_, err = cc.IntentionUpdate(i, nil)
		if err != nil {
			return nil, err
		}
		return i, nil
	} else {
		id, _, err := cc.IntentionCreate(i, nil)
		if err != nil {
			return nil, err
		}
		i.ID = id
		return i, nil
	}

}

func getIntentionByKey(client *api.Client, key string) (*api.Intention, error) {
	all, err := dumpIntentions(client)
	if err != nil {
		return nil, err
	}

	i, ok := all[key]
	if !ok {
		return nil, nil
	}
	return i, nil
}

func dumpIntentions(client *api.Client) (map[string]*api.Intention, error) {
	cc := client.Connect()

	all, _, err := cc.Intentions(nil)
	if err != nil {
		return nil, err
	}

	m := make(map[string]*api.Intention)
	for _, i := range all {
		m[intentionKey(i)] = i
	}

	return m, nil
}

func intentionKey(i *api.Intention) string {
	return i.SourceName + ">" + i.DestinationName
}
