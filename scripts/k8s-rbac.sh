#!/bin/bash

set -euo pipefail

# Remove undesirable side effects of CDPATH variable
unset CDPATH

die() {
    echo "ERROR: $1" >&2
    exit 1
}

if ! minikube status >/dev/null 2>&1 ; then
    die "minikube is not running; please run it as something like 'minikube start --memory=4096'"
fi

cd "$(dirname "$(dirname "$0")")"

mkdir -p cache/k8s

kubectl config view -o jsonpath='{.clusters[?(@.name == "minikube")].cluster.server}' > cache/k8s/config_host
ca_file="$(kubectl config view -o jsonpath='{.clusters[?(@.name == "minikube")].cluster.certificate-authority}')"
if [[ -z "${ca_file}" ]]; then
    die "no minikube ca file found"
fi
if [[ -n "$${ca_file}" ]]; then
    cp -f "${ca_file}" cache/k8s/config_ca
fi

readonly sa_name="consul-server-auth-method"

echo ">>> switching to minikube kubectl context"
kubectl config use-context minikube

echo ">>> creating RBAC entities for '${sa_name}'"
cat > cache/k8s/k8s-rbac-boot.yml <<EOF
---
kind: ClusterRoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: review-tokens
  namespace: default
subjects:
- kind: ServiceAccount
  name: ${sa_name}
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
  name: ${sa_name}
  namespace: default
roleRef:
  kind: ClusterRole
  name: service-account-getter
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${sa_name}
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
EOF
kubectl apply -f cache/k8s/k8s-rbac-boot.yml
# kubectl delete -f cache/k8s/k8s-rbac-boot.yml
# exit 0
rm -f cache/k8s/k8s-rbac-boot.yml

# extract the JWT from the service account
secret_name="$(kubectl get sa "${sa_name}" -o jsonpath='{.secrets[0].name}')"
kubectl get secret "${secret_name}" -o go-template='{{ .data.token | base64decode }}' > cache/k8s/jwt_token

# also get secrets for service accounts in pods
for name in ping pong ; do
    secret_name="$(kubectl get sa "${name}" -o jsonpath='{.secrets[0].name}')"
    kubectl get secret "${secret_name}" -o go-template='{{ .data.token | base64decode }}' > "cache/k8s/service_jwt_token.${name}"
done
