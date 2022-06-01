package main

import "text/template"

var serviceRegistrationT = template.Must(template.New("service_reg").Parse(`
services = [
  {
    name = "{{.Service.ID.Name}}"
{{- if .EnterpriseEnabled }}
    namespace = "{{.Service.ID.Namespace}}"
{{- if not .EnterpriseDisablePartitions }}
    partition = "{{.Service.ID.Partition}}"
{{- end }}
{{- end }}
    port = {{.Service.Port}}

    checks = [
      {
        name     = "up"
        http     = "http://localhost:{{.Service.Port}}/healthz"
        method   = "GET"
        interval = "5s"
        timeout  = "1s"
      },
    ]

    meta {
{{- range $k, $v := .Service.Meta }}
      "{{ $k }}" = "{{ $v }}",
{{- end }}
    }

    connect {
      sidecar_service {
        proxy {
          upstreams = [
            {
              destination_name = "{{.Service.UpstreamID.Name}}"
{{- if .EnterpriseEnabled }}
              destination_namespace = "{{.Service.UpstreamID.Namespace}}"
{{- if not .EnterpriseDisablePartitions }}
              destination_partition = "{{.Service.UpstreamID.Partition}}"
{{- end }}
{{- end }}
              local_bind_port  = {{.Service.UpstreamLocalPort}}
{{- if .Service.UpstreamCluster }}
{{- if .LinkWithFederation }}
              datacenter = "{{.Service.UpstreamCluster}}"
{{- end }}
{{- if .LinkWithPeering }}
              destination_peer = "{{.Service.UpstreamCluster}}"
{{- end }}
{{- end }}
{{ .Service.UpstreamExtraHCL }}
            },
          ]
        }
      }
    }
  },
]
`))
