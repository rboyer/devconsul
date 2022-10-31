package app

import "github.com/hashicorp/go-uuid"

func (a *App) maybeInitAgentMasterToken() error {
	if a.config.SecurityDisableACLs {
		return a.cache.DelValue("agent-master-token")
	}

	var err error
	a.config.AgentMasterToken, err = a.cache.LoadOrSaveValue("agent-master-token", func() (string, error) {
		return uuid.GenerateUUID()
	})
	return err
}
