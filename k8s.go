package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/rboyer/safeio"
	"golang.org/x/crypto/blake2b"
)

func (c *Core) runK8SInit() error {
	if !c.config.KubernetesEnabled {
		_ = os.RemoveAll("cache/k8s")
		return nil
	}

	if err := os.MkdirAll("cache/k8s", 0755); err != nil {
		return err
	}

	// Check to see if we have any work to do.
	var currentHash string
	{
		hash, err := blake2b.New256(nil)
		if err != nil {
			return err
		}

		if err := addFileToHash(c.devconsulBin, hash); err != nil {
			return err
		}
		if err := addFileToHash(defaultConfigFile, hash); err != nil {
			return err
		}

		currentHash = fmt.Sprintf("%x", hash.Sum(nil))
	}

	var priorHash string
	{
		b, err := os.ReadFile("cache/k8s.hash")
		if os.IsNotExist(err) {
			priorHash = ""
		} else if err != nil {
			return err
		} else {
			priorHash = string(b)
		}
	}

	if priorHash == currentHash {
		c.logger.Info("skipping k8s setup generation")
		return nil
	}

	if err := c.realRunK8SInit(); err != nil {
		return err
	}

	// Checkpoint
	if _, err := safeio.WriteToFile(bytes.NewReader([]byte(currentHash)), "cache/k8s.hash", 0644); err != nil {
		return err
	}

	return nil
}

func (c *Core) realRunK8SInit() error {
	var err error

	if err := cmdExec("minikube", c.minikubeBin, []string{"status"}, io.Discard); err != nil {
		return fmt.Errorf("minikube is not running; please run it as something like 'minikube start --memory=4096': %v", err)
	}

	if err := c.writeK8SConfigHost(); err != nil {
		return err
	}
	if err := c.writeK8SConfigCACert(); err != nil {
		return err
	}

	c.logger.Info(">>> switching to minikube kubectl context")
	if err := cmdExec("kubectl", c.kubectlBin, []string{
		"config", "use-context", "minikube",
	}, io.Discard); err != nil {
		return err
	}

	const saName = "consul-server-auth-method"

	c.logger.Info(">>> creating RBAC entities", "serviceaccount", saName)
	_, err = safeio.WriteToFile(
		bytes.NewBuffer([]byte(fmt.Sprintf(kubeRBACTemplate, saName))),
		"cache/k8s/k8s-rbac-boot.yml",
		0644,
	)
	if err != nil {
		return err
	}
	if err := cmdExec("kubectl", c.kubectlBin, []string{
		"apply", "-f", "cache/k8s/k8s-rbac-boot.yml",
	}, nil); err != nil {
		return err
	}
	if err := os.Remove("cache/k8s/k8s-rbac-boot.yml"); err != nil {
		return err
	}

	// extract the JWT from the service account
	if err := c.writeServiceAccountSecret(saName, "cache/k8s/jwt_token"); err != nil {
		return err
	}

	// also get secrets for service accounts in pods
	if err := c.writeServiceAccountSecret("ping", "cache/k8s/service_jwt_token.ping"); err != nil {
		return err
	}
	if err := c.writeServiceAccountSecret("pong", "cache/k8s/service_jwt_token.pong"); err != nil {
		return err
	}

	return nil
}

func (c *Core) writeServiceAccountSecret(saName, filename string) error {
	// extract the JWT from the service account
	secretName, err := c.getServiceAccountSecretName(saName)
	if err != nil {
		return err
	}

	return c.writeK8SSecretFile(secretName, filename)
}

func (c *Core) writeK8SSecretFile(secretName, filename string) error {
	w, err := safeio.OpenFile(filename, 0644)
	if err != nil {
		return err
	}
	defer w.Close()

	if err := cmdExec("kubectl", c.kubectlBin, []string{
		"get", "secret", secretName, "-o", "go-template={{ .data.token | base64decode }}",
	}, w); err != nil {
		return err
	}

	return w.Commit()
}

func (c *Core) getServiceAccountSecretName(saName string) (string, error) {
	// secret_name="$(kubectl get sa "${sa_name}" -o jsonpath='{.secrets[0].name}')"

	var out bytes.Buffer
	if err := cmdExec("kubectl", c.kubectlBin, []string{
		"get", "sa", saName, "-o", "jsonpath={.secrets[0].name}",
	}, &out); err != nil {
		return "", err
	}

	return out.String(), nil
}

func (c *Core) writeK8SConfigHost() error {
	// kubectl config view -o jsonpath='{.clusters[?(@.name == "minikube")].cluster.server}' > cache/k8s/config_host

	w, err := safeio.OpenFile("cache/k8s/config_host", 0644)
	if err != nil {
		return err
	}
	defer w.Close()

	if err := cmdExec("kubectl", c.kubectlBin, []string{
		"config",
		"view",
		"-o",
		`jsonpath={.clusters[?(@.name == "minikube")].cluster.server}`,
	}, w); err != nil {
		return err
	}

	return w.Commit()
}

func (c *Core) writeK8SConfigCACert() error {
	// ca_file="$(kubectl config view -o jsonpath='{.clusters[?(@.name == "minikube")].cluster.certificate-authority}')"

	var out bytes.Buffer
	if err := cmdExec("kubectl", c.kubectlBin, []string{
		"config",
		"view",
		"-o",
		`jsonpath={.clusters[?(@.name == "minikube")].cluster.certificate-authority}`,
	}, &out); err != nil {
		return err
	}
	caFile := out.String()
	if caFile == "" {
		return fmt.Errorf("no minikube ca file found")
	}

	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return err
	}

	_, err = safeio.WriteToFile(bytes.NewBuffer(caCert), "cache/k8s/config_ca", 0644)
	return err
}

const kubeRBACTemplate = `---
kind: ClusterRoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: review-tokens
  namespace: default
subjects:
- kind: ServiceAccount
  name: %[1]s
  namespace: default
roleRef:
  kind: ClusterRole
  name: system:auth-delegator
  apiGroup: rbac.authorization.k8s.io
---
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: service-account-getter
  namespace: default
rules:
- apiGroups: [""]
  resources: ["serviceaccounts"]
  verbs: ["get"]
---
kind: ClusterRoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: get-service-accounts
  namespace: default
subjects:
- kind: ServiceAccount
  name: %[1]s
  namespace: default
roleRef:
  kind: ClusterRole
  name: service-account-getter
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %[1]s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ping
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: pong
`
