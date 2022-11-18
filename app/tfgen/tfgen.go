package tfgen

import (
	"embed"
)

//go:embed templates/container-pause.tf.tmpl
//go:embed templates/container-consul.tf.tmpl
//go:embed templates/container-mgw.tf.tmpl
//go:embed templates/container-app.tf.tmpl
//go:embed templates/container-app-sidecar.tf.tmpl
//go:embed templates/prometheus-config.yml.tmpl
//go:embed templates/container-prometheus.tf
//go:embed templates/container-grafana.tf
//go:embed templates/grafana-prometheus.yml
//go:embed templates/grafana.ini
//go:embed templates/container-vault.tf
//go:embed templates/vault-config.hcl
var content embed.FS
