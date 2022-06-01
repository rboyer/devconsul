package main

import "fmt"

// TODO: rename file to porcelain

func (c *Core) RunBringUp() error {
	return c.runBringUp(false)
}

func (c *Core) RunBringUpPrimary() error {
	return c.runBringUp(true)
}

func (c *Core) runBringUp(primaryOnly bool) error {
	if primaryOnly && c.topology.LinkWithPeering() {
		return fmt.Errorf("primary only is not available with peering")
	}
	if err := c.runInit(); err != nil {
		return err
	}

	if err := c.runGenerate(primaryOnly); err != nil {
		return err
	}

	if err := c.runBoot(primaryOnly); err != nil {
		return err
	}

	return nil
}

func (c *Core) RunBringDown() error {
	return c.destroy(false)
}

func (c *Core) runInit() error {
	return runOnce("init", func() error {
		if err := c.runDocker(); err != nil {
			return err
		}
		if err := c.runK8SInit(); err != nil {
			return err
		}
		return nil
	})
}

func (c *Core) RunRestart() error {
	// This only makes sense to run after you've configured it once.
	if err := checkHasRunOnce("init"); err != nil {
		return err
	}

	if err := c.stopAllContainers(); err != nil {
		return err
	}

	return c.RunBringUp()
}

func (c *Core) RunRestartDC2() error {
	// This only makes sense to run after you've configured it once.
	if err := checkHasRunOnce("init"); err != nil {
		return err
	}

	if err := c.runStopDC2(); err != nil {
		return err
	}

	return c.RunBringUp()
}
