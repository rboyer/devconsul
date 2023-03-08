package tfgen

import (
	"text/template"

	"github.com/rboyer/devconsul/config"
	"github.com/rboyer/devconsul/infra"
)

func GenerateMeshGatewayContainer(
	config *config.Config,
	topology *infra.Topology,
	podName string,
	node *infra.Node,
) Resource {
	if !node.MeshGateway {
		return nil
	}

	switch node.Kind {
	case infra.NodeKindClient:
	default:
		panic("figure this out: " + node.Kind)
	}

	type tfMeshGatewayInfo struct {
		PodName            string
		NodeName           string
		EnvoyLogLevel      string
		EnableACLs         bool
		EnableWAN          bool
		LANAddress         string
		WANAddress         string
		ExposeServers      bool
		SidecarBootEnvVars []string
		Labels             map[string]string
	}

	mgi := tfMeshGatewayInfo{
		PodName:       podName,
		NodeName:      node.Name,
		EnvoyLogLevel: config.EnvoyLogLevel,
		EnableACLs:    !config.SecurityDisableACLs,
		Labels:        map[string]string{
			//
		},
		SidecarBootEnvVars: []string{
			//
		},
	}
	node.AddLabels(mgi.Labels)

	if config.SecurityDisableACLs {
		mgi.SidecarBootEnvVars = append(mgi.SidecarBootEnvVars,
			"SBOOT_MODE=insecure",
		)
	} else {
		mgi.SidecarBootEnvVars = append(mgi.SidecarBootEnvVars,
			"SBOOT_MODE=direct",
			"SBOOT_TOKEN_FILE=/secrets/mesh-gateway--"+node.Cluster+".val",
		)
	}

	if config.EnterpriseEnabled && node.Partition != "" {
		mgi.SidecarBootEnvVars = append(mgi.SidecarBootEnvVars,
			"SBOOT_PARTITION="+node.Partition)
	}

	if config.EncryptionTLSAPI {
		mgi.SidecarBootEnvVars = append(mgi.SidecarBootEnvVars,
			"SBOOT_AGENT_TLS=1")
	}
	if config.EncryptionTLSGRPC {
		mgi.SidecarBootEnvVars = append(mgi.SidecarBootEnvVars,
			"SBOOT_AGENT_GRPC_TLS=1")
	}

	switch topology.NetworkShape {
	case infra.NetworkShapeIslands, infra.NetworkShapeDual:
		mgi.EnableWAN = true
		mgi.ExposeServers = true
		mgi.LANAddress = `{{ GetInterfaceIP \"eth0\" }}:8443`
		mgi.WANAddress = `{{ GetInterfaceIP \"eth1\" }}:8443`
	case infra.NetworkShapeFlat:
		mgi.EnableWAN = true
		mgi.LANAddress = `{{ GetInterfaceIP \"eth0\" }}:8443`
		if node.MeshGatewayUseDNSWANAddress {
			mgi.WANAddress = node.PodName() + ":8443"
		} else {
			mgi.WANAddress = `{{ GetInterfaceIP \"eth0\" }}:8443`
		}
	default:
		panic("unknown shape: " + topology.NetworkShape)
	}

	return Eval(tfMeshGatewayT, &mgi)
}

var tfMeshGatewayT = template.Must(template.ParseFS(content, "templates/container-mgw.tf.tmpl"))
