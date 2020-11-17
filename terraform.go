package main

import "os"

func (c *Core) terraformApply() error {
	if _, err := os.Stat(".terraform"); err != nil {
		if !os.IsNotExist(err) {
			return err
		}

		// On the fly init
		c.logger.Info("Running 'terraform init'...")
		if err := cmdExec("terraform", c.tfBin, []string{"init"}, nil); err != nil {
			return err
		}
	}

	c.logger.Info("Running 'terraform apply'...")
	return cmdExec("terraform", c.tfBin, []string{"apply", "-auto-approve"}, nil)
}

func (c *Core) terraformDestroy() error {
	c.logger.Info("Running 'terraform destroy'...")
	return cmdExec("terraform", c.tfBin, []string{
		"destroy", "-auto-approve", "-refresh=false",
	}, nil)
}
