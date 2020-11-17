package main

import (
	"os"
	"path/filepath"

	"github.com/hashicorp/go-multierror"
)

func (c *Core) destroy(_ bool) error {
	c.logger.Info("destroying everything")

	if err := c.terraformDestroy(); err != nil {
		return err
	}

	files := []string{
		"terraform.tfstate",
		"terraform.tfstate.backup",
		"cache/grafana-prometheus.yml",
		"cache/grafana.ini",
		"cache/prometheus.yml",
	}

	for _, patt := range []string{
		"cache/*.val",
		"cache/*.hcl",
		"cache/*.tf",
		"cache/*.hash",
		"cache/*.done",
	} {
		m, err := filepath.Glob(patt)
		if err != nil {
			return err
		}
		files = append(files, m...)
	}

	var merr *multierror.Error

	for _, dir := range []string{"cache/k8s", "cache/tls"} {
		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue // skip
		} else if err != nil {
			merr = multierror.Append(merr, err)
			continue
		}

		err = os.RemoveAll(dir)
		if err == nil {
			c.logger.Info("deleted directory", "path", dir)
		}
		merr = multierror.Append(merr, err)
	}

	for _, fn := range files {
		err := os.Remove(fn)
		if err == nil {
			c.logger.Info("deleted file", "path", fn)
		}
		if !os.IsNotExist(err) {
			merr = multierror.Append(merr, err)
			continue
		}
	}

	return merr.ErrorOrNil()
}
