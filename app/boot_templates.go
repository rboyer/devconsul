package app

import "text/template"

var serviceRegistrationT = template.Must(template.New("service_reg").Parse(`
services = [
  {
    name = "{{.Service.ID.Name}}"
{{- if .EnterpriseEnabled }}
    namespace = "{{.Service.ID.Namespace}}"
    partition = "{{.Service.ID.Partition}}"
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
              destination_partition = "{{.Service.UpstreamID.Partition}}"
{{- end }}
              local_bind_port  = {{.Service.UpstreamLocalPort}}
{{- if .Service.UpstreamDatacenter }}
              datacenter = "{{.Service.UpstreamDatacenter}}"
{{- end }}
{{- if .Service.UpstreamPeer }}
              destination_peer = "{{.Service.UpstreamPeer}}"
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
