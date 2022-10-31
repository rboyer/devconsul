package app

import "fmt"

func (a *App) RunBringUp() error {
	return a.runBringUp(false)
}

func (a *App) RunBringUpPrimary() error {
	return a.runBringUp(true)
}

func (a *App) runBringUp(primaryOnly bool) error {
	if primaryOnly && a.topology.LinkWithPeering() {
		return fmt.Errorf("primary only is not available with peering")
	}

	if err := a.maybeInitTLS(); err != nil {
		return err
	}
	if err := a.maybeInitGossipKey(); err != nil {
		return err
	}
	if err := a.maybeInitAgentMasterToken(); err != nil {
		return err
	}

	// Legit needed exactly one time.
	err := runOnce("init", func() error {
		if err := a.buildDockerImages(false); err != nil {
			return err
		}
		if err := a.runK8SInit(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := a.runGenerate(primaryOnly); err != nil {
		return err
	}

	if err := a.runBoot(primaryOnly); err != nil {
		return err
	}

	return nil
}

func (a *App) RunBringDown() error {
	return a.destroy(false)
}

func (a *App) RunRestart() error {
	// This only makes sense to run after you've configured it once.
	if err := checkHasInitRunOnce(); err != nil {
		return err
	}

	if err := a.stopAllContainers(); err != nil {
		return err
	}

	return a.RunBringUp()
}

func (c *Core) RunRestartDC2() error {
	// This only makes sense to run after you've configured it once.
	if err := checkHasInitRunOnce(); err != nil {
		return err
	}

	if err := c.runStopDC2(); err != nil {
		return err
	}

	return c.RunBringUp()
}
