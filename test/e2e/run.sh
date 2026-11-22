#!/usr/bin/env bash
set -euo pipefail

cluster=openbao-rotation-reminders-e2e
image=openbao-rotation-reminders:e2e
topic="e2e-$(openssl rand -hex 8)"
work=$(mktemp -d)

cleanup() {
  status=$?
  if (( status != 0 )); then
    kubectl get pods --all-namespaces || true
    kubectl --namespace rotation describe jobs,pods || true
    kubectl --namespace rotation logs --selector app.kubernetes.io/name=openbao-rotation-reminders --tail=100 || true
    kubectl --namespace openbao logs deployment/openbao --tail=50 || true
  fi
  kind delete cluster --name "${cluster}"
  rm -rf "${work}"
  exit "${status}"
}

kind create cluster --name "${cluster}" --wait 180s
trap cleanup EXIT

docker build --build-arg VERSION=e2e --tag "${image}" .
kind load docker-image --name "${cluster}" "${image}"

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=openbao-e2e-ca" \
  -keyout "${work}/ca.key" -out "${work}/ca.crt" 2> /dev/null
openssl req -newkey rsa:2048 -nodes -subj "/CN=openbao.openbao.svc" \
  -keyout "${work}/tls.key" -out "${work}/tls.csr" 2> /dev/null
printf 'subjectAltName=DNS:openbao.openbao.svc\nextendedKeyUsage=serverAuth\n' > "${work}/tls.ext"
openssl x509 -req -days 1 -in "${work}/tls.csr" -CA "${work}/ca.crt" -CAkey "${work}/ca.key" \
  -CAcreateserial -extfile "${work}/tls.ext" -out "${work}/tls.crt" 2> /dev/null

kubectl create namespace openbao
kubectl --namespace openbao create secret tls openbao-tls --cert "${work}/tls.crt" --key "${work}/tls.key"
kubectl apply --filename test/e2e/openbao.yaml --filename test/e2e/ntfy.yaml
kubectl --namespace openbao rollout status deployment/openbao --timeout=180s
kubectl --namespace ntfy rollout status deployment/ntfy --timeout=180s

bao() {
  kubectl --namespace openbao exec --stdin deployment/openbao -- bao "$@"
}

bao secrets enable -path=e2e -version=2 kv
bao kv put -mount=e2e app/expired value=do-not-read
bao kv metadata put -mount=e2e -custom-metadata=expires_at=2020-01-01 \
  -custom-metadata="rotation_note=Regenerate it upstream" app/expired
bao kv put -mount=e2e app/fine value=do-not-read
bao kv metadata put -mount=e2e -custom-metadata=rotation_interval=90d app/fine
bao kv put -mount=e2e app/typo value=do-not-read
bao kv metadata put -mount=e2e -custom-metadata=rotation_intervall=30d app/typo
bao kv put -mount=e2e app/untracked value=do-not-read
bao auth enable kubernetes
bao write auth/kubernetes/config kubernetes_host=https://kubernetes.default.svc
sed 's/MOUNT/e2e/' examples/policy.hcl | bao policy write openbao-rotation-reminders -
bao write auth/kubernetes/role/openbao-rotation-reminders \
  bound_service_account_names=openbao-rotation-reminders \
  bound_service_account_namespaces=rotation \
  audience=openbao \
  token_ttl=5m \
  token_max_ttl=5m \
  token_policies=openbao-rotation-reminders \
  token_no_default_policy=true

kubectl apply --filename test/e2e/namespace.yaml
kubectl --namespace rotation create secret generic openbao-ca --from-file=ca.crt="${work}/ca.crt"
kubectl --namespace rotation create secret generic openbao-rotation-reminders-ntfy --from-literal=NTFY_TOPIC="${topic}"
helm install openbao-rotation-reminders charts/openbao-rotation-reminders \
  --namespace rotation \
  --values test/e2e/values.yaml

token_count() {
  bao list -format=json auth/token/accessors | grep --count '"'
}
tokens_before=$(token_count)

kubectl --namespace rotation create job e2e --from=cronjob/openbao-rotation-reminders
kubectl --namespace rotation wait job/e2e --for=condition=Complete --timeout=180s
logs=$(kubectl --namespace rotation logs job/e2e)
echo "${logs}"

expected_logs="scanned=4 tracked=3 findings=2
--- OpenBao: 1 expired, 1 invalid (priority=urgent) ---
EXPIRED:
- app/expired: expired 2020-01-01 ("
if [[ "${logs}" != "${expected_logs}"* ]]; then
  echo "unexpected job output" >&2
  exit 1
fi
if grep --fixed-strings --quiet "${topic}" <<< "${logs}"; then
  echo "the job output contains the topic" >&2
  exit 1
fi

tokens_after=$(token_count)
if (( tokens_after != tokens_before )); then
  echo "tokens went from ${tokens_before} to ${tokens_after}" >&2
  exit 1
fi

messages=$(kubectl get --raw "/api/v1/namespaces/ntfy/services/ntfy:http/proxy/${topic}/json?poll=1")
echo "${messages}"
if [[ "$(grep --count '"event":"message"' <<< "${messages}")" != 1 ]]; then
  echo "expected one ntfy message" >&2
  exit 1
fi
message=$(grep '"event":"message"' <<< "${messages}")
field() {
  yq --input-format json --output-format yaml "$1" <<< "${message}"
}
expect() {
  if [[ "$(field "$1")" != "$2" ]]; then
    echo "ntfy message $1: expected '$2', got '$(field "$1")'" >&2
    exit 1
  fi
}
expect .title "OpenBao: 1 expired, 1 invalid"
expect .priority 5
expect '.tags | join(",")' "rotating_light,key"
expect .click "https://bao.example.com"
expect '.message | split("\n") | .[2]' "  Note: Regenerate it upstream"
expect '.message | split("\n") | .[5]' '- app/typo: metadata error: unknown metadata key "rotation_intervall"'

echo "e2e passed"
