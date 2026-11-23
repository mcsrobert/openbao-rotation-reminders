#!/usr/bin/env bash
set -euo pipefail

chart=charts/openbao-rotation-reminders
values=test/chart/values.yaml
release=openbao-rotation-reminders
namespace=security

render() {
  helm template "${release}" "${chart}" --namespace "${namespace}" --values "${values}" "$@"
}

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

expect_equal() {
  if [[ "$2" != "$3" ]]; then
    fail "$1: expected '$3', got '$2'"
  fi
}

expect_rejected() {
  local description=$1
  shift
  if helm template "${release}" "${chart}" --namespace "${namespace}" "$@" > /dev/null 2>&1; then
    fail "the chart accepted ${description}"
  fi
}

all_options=(
  --set networkPolicy.enabled=true
  --set networkPolicy.openbao.namespaceSelector.matchLabels.kubernetes\\.io/metadata\\.name=openbao
  --set 'networkPolicy.extraEgress[0].ports[0].port=80'
  --set openbao.tls.caSecretName=openbao-internal-tls
  --set openbao.uiUrl=https://bao.example.com
  --set timeZone=Europe/Amsterdam
)

helm lint --strict "${chart}" --values "${values}"
helm lint --strict "${chart}" --values "${values}" "${all_options[@]}"

render | kubeconform -strict -summary -schema-location default
render "${all_options[@]}" | kubeconform -strict -summary -schema-location default

cronjob() {
  local expression=$1
  shift
  render --show-only templates/cronjob.yaml "$@" | yq "${expression}"
}
pod=.spec.jobTemplate.spec.template.spec
container="${pod}.containers[0]"
env() {
  local name=$1
  shift
  cronjob "${container}.env[] | select(.name == \"${name}\") | (.value // .valueFrom.secretKeyRef.name + \"/\" + .valueFrom.secretKeyRef.key)" "$@"
}

digest=sha256:0000000000000000000000000000000000000000000000000000000000000000
image=ghcr.io/mcsrobert/openbao-rotation-reminders
app_version=$(yq '.appVersion' "${chart}/Chart.yaml")
expect_equal "image without a digest" "$(cronjob "${container}.image")" "${image}:${app_version}"
expect_equal "image with a digest" "$(cronjob "${container}.image" --set image.tag=1.2.3 --set "image.digest=${digest}")" "${image}:1.2.3@${digest}"

expect_equal "schedule" "$(cronjob '.spec.schedule')" "0 9 * * *"
expect_equal "no time zone" "$(cronjob '.spec.timeZone')" "null"
expect_equal "time zone" "$(cronjob '.spec.timeZone' --set timeZone=Europe/Amsterdam)" "Europe/Amsterdam"
expect_equal "job settings" \
  "$(cronjob '[.spec.concurrencyPolicy, .spec.startingDeadlineSeconds, .spec.successfulJobsHistoryLimit, .spec.failedJobsHistoryLimit, .spec.suspend, .spec.jobTemplate.spec.activeDeadlineSeconds, .spec.jobTemplate.spec.backoffLimit] | join(",")')" \
  "Forbid,300,3,3,false,600,1"
expect_equal "restart policy" "$(cronjob "${pod}.restartPolicy")" "Never"
expect_equal "service account token" "$(cronjob "${pod}.automountServiceAccountToken")" "false"
expect_equal "service account" "$(cronjob "${pod}.serviceAccountName")" "openbao-rotation-reminders"
expect_equal "service account automount" \
  "$(render --show-only templates/service-account.yaml | yq '.automountServiceAccountToken')" \
  "false"

expect_equal "OpenBao address" "$(env OPENBAO_ADDR)" "https://openbao.security.svc.cluster.local:8200"
expect_equal "OpenBao mount" "$(env OPENBAO_MOUNT)" "homelab"
expect_equal "default role" "$(env OPENBAO_K8S_ROLE)" "openbao-rotation-reminders"
expect_equal "role" "$(env OPENBAO_K8S_ROLE --set openbao.auth.role=custom)" "custom"
expect_equal "role of another release" \
  "$(helm template other "${chart}" --values "${values}" --show-only templates/cronjob.yaml | yq "${container}.env[] | select(.name == \"OPENBAO_K8S_ROLE\") | .value")" \
  "other-openbao-rotation-reminders"
expect_equal "JWT file" "$(env OPENBAO_JWT_FILE)" "/var/run/secrets/openbao/token"
expect_equal "ntfy URL" "$(env NTFY_URL)" "https://ntfy.sh"
expect_equal "ntfy topic" "$(env NTFY_TOPIC)" "openbao-rotation-reminders-ntfy/NTFY_TOPIC"
expect_equal "default warning" "$(env DEFAULT_EXPIRES_WARN_BEFORE)" "14d"
expect_equal "no CA file" "$(env OPENBAO_CA_FILE)" ""
expect_equal "no UI URL" "$(env OPENBAO_UI_URL)" ""
expect_equal "UI URL" "$(env OPENBAO_UI_URL --set openbao.uiUrl=https://bao.example.com)" "https://bao.example.com"

expect_equal "projected token" \
  "$(cronjob "${pod}.volumes[] | select(.name == \"openbao-token\") | .projected.sources[0].serviceAccountToken | [.path, .audience, .expirationSeconds] | join(\",\")")" \
  "token,openbao,600"
expect_equal "token mount" \
  "$(cronjob "${container}.volumeMounts[] | select(.name == \"openbao-token\") | [.mountPath, .readOnly] | join(\",\")")" \
  "/var/run/secrets/openbao,true"
expect_equal "no CA volume" "$(cronjob "${pod}.volumes | length")" "1"

ca=(--set openbao.tls.caSecretName=openbao-internal-tls)
expect_equal "CA file" "$(env OPENBAO_CA_FILE "${ca[@]}")" "/etc/openbao/ca.crt"
expect_equal "CA volume" \
  "$(cronjob "${pod}.volumes[] | select(.name == \"openbao-ca\") | .secret | [.secretName, .items[0].key, .items[0].path] | join(\",\")" "${ca[@]}")" \
  "openbao-internal-tls,ca.crt,ca.crt"
expect_equal "CA mount" \
  "$(cronjob "${container}.volumeMounts[] | select(.name == \"openbao-ca\") | [.mountPath, .readOnly] | join(\",\")" "${ca[@]}")" \
  "/etc/openbao,true"

expect_equal "pod security context" \
  "$(cronjob "${pod}.securityContext | [.runAsNonRoot, .runAsUser, .runAsGroup, .seccompProfile.type] | join(\",\")")" \
  "true,65532,65532,RuntimeDefault"
expect_equal "container security context" \
  "$(cronjob "${container}.securityContext | [.allowPrivilegeEscalation, .readOnlyRootFilesystem, (.capabilities.drop | join(\"+\"))] | join(\",\")")" \
  "false,true,ALL"
expect_equal "resources" \
  "$(cronjob "${container}.resources | [.requests.cpu, .requests.memory, .limits.memory, .limits.cpu] | join(\",\")")" \
  "10m,32Mi,64Mi,"

kinds() {
  render "$@" | yq --no-doc '.kind' | sort | paste -sd, -
}
expect_equal "default resources" "$(kinds)" "CronJob,ServiceAccount"
expect_equal "without a service account" "$(kinds --set serviceAccount.create=false)" "CronJob"
expect_equal "with a network policy" "$(kinds --set networkPolicy.enabled=true)" "CronJob,NetworkPolicy,ServiceAccount"

policy() {
  local expression=$1
  shift
  render --show-only templates/network-policy.yaml --set networkPolicy.enabled=true "$@" | yq "${expression}"
}
expect_equal "policy pod selector" \
  "$(policy '.spec.podSelector.matchLabels | to_entries | map(.key + "=" + .value) | join(",")')" \
  "app.kubernetes.io/name=openbao-rotation-reminders,app.kubernetes.io/instance=openbao-rotation-reminders"
expect_equal "policy types" "$(policy '.spec.policyTypes | join(",")')" "Ingress,Egress"
expect_equal "no ingress" "$(policy '.spec.ingress | length')" "0"
expect_equal "DNS egress" \
  "$(policy '.spec.egress[0] | [.to[0].namespaceSelector.matchLabels["kubernetes.io/metadata.name"], .to[0].podSelector.matchLabels["k8s-app"], (.ports | map(.port + "/" + .protocol) | join("+"))] | join(",")')" \
  "kube-system,kube-dns,53/UDP+53/TCP"
expect_equal "OpenBao egress in the release namespace" \
  "$(policy '.spec.egress[1] | [(.to[0].podSelector.matchLabels | to_entries | map(.key + "=" + .value) | join("+")), .to[0].namespaceSelector, .ports[0].port, .ports[0].protocol] | join(",")')" \
  "app.kubernetes.io/name=openbao+component=server,,8200,TCP"
expect_equal "OpenBao egress in another namespace" \
  "$(policy '.spec.egress[1] | [.to[0].namespaceSelector.matchLabels.team, .ports[0].port] | join(",")' --set networkPolicy.openbao.namespaceSelector.matchLabels.team=vault --set networkPolicy.openbao.port=8300)" \
  "vault,8300"
expect_equal "public HTTPS egress" \
  "$(policy '.spec.egress[2] | [(.to | map(.ipBlock.cidr + "-" + (.ipBlock.except | join("-"))) | join("+")), .ports[0].port, .ports[0].protocol] | join(",")')" \
  "0.0.0.0/0-10.0.0.0/8-100.64.0.0/10-127.0.0.0/8-169.254.0.0/16-172.16.0.0/12-192.168.0.0/16+::/0-::1/128-fc00::/7-fe80::/10,443,TCP"
expect_equal "no extra egress" "$(policy '.spec.egress | length')" "3"
expect_equal "extra egress" "$(policy '.spec.egress[3].ports[0].port' --set 'networkPolicy.extraEgress[0].ports[0].port=80')" "80"

names() {
  helm template "$@" "${chart}" --namespace "${namespace}" --values "${values}" \
    | yq --no-doc '[.metadata.name, .spec.jobTemplate.spec.template.spec.serviceAccountName // "", (.spec.jobTemplate.spec.template.spec.containers[0].env[] | select(.name == "OPENBAO_K8S_ROLE") | .value) // ""] | map(select(. != "")) | join(",")' \
    | paste -sd, -
}
long_name=production-rotation-reminder-openbao-rotation-remind
expect_equal "names of a long release" "$(names production-rotation-reminder)" \
  "${long_name},${long_name},${long_name},${long_name}"
expect_equal "names with a long override" \
  "$(names "${release}" --set fullnameOverride="$(printf 'a%.0s' {1..60})")" \
  "$(printf 'a%.0s' {1..52} | sed 's/.*/&,&,&,&/')"

pod_labels() {
  cronjob "${pod%.spec}.metadata.labels | to_entries | map(.key + \"=\" + .value) | join(\",\")" "$@"
}
expect_equal "pod labels" \
  "$(pod_labels --set podLabels.team=platform --set-string podLabels.app\\.kubernetes\\.io/version=custom)" \
  "app.kubernetes.io/instance=${release},app.kubernetes.io/managed-by=Helm,app.kubernetes.io/name=openbao-rotation-reminders,app.kubernetes.io/version=custom,helm.sh/chart=openbao-rotation-reminders-$(yq '.version' "${chart}/Chart.yaml"),team=platform"

numeric=(
  --set-string ntfy.existingSecret=1234
  --set-string ntfy.existingSecretKey=1
  --set-string openbao.tls.caSecretName=5678
  --set-string openbao.tls.caSecretKey=2
  --set-string priorityClassName=10
)
helm template 1234 "${chart}" --namespace "${namespace}" --values "${values}" "${numeric[@]}" \
  | kubeconform -strict -summary -schema-location default
expect_equal "numeric Secret names" "$(env NTFY_TOPIC "${numeric[@]}")" "1234/1"

for mount in .hidden/kv team/..kv /homelab/; do
  helm template "${release}" "${chart}" --namespace "${namespace}" --values "${values}" --set "openbao.mount=${mount}" > /dev/null \
    || fail "the chart rejected the mount ${mount}"
done

expect_rejected "missing values"
expect_rejected "an unknown value" --values "${values}" --set unknown=true
expect_rejected "an unknown OpenBao field" --values "${values}" --set openbao.unknown=true
expect_rejected "a missing address" --values "${values}" --set openbao.address=
expect_rejected "an address with a query" --values "${values}" --set 'openbao.address=https://openbao?x=1'
expect_rejected "an address with credentials" --values "${values}" --set 'openbao.address=https://user:pass@openbao'
expect_rejected "an empty mount" --values "${values}" --set openbao.mount=
for mount in . .. team/../kv team/./kv team/..; do
  expect_rejected "the mount ${mount}" --values "${values}" --set "openbao.mount=${mount}"
done
expect_rejected "a pod label that selects the release" --values "${values}" --set podLabels.app\\.kubernetes\\.io/instance=other
expect_rejected "a missing ntfy secret" --values "${values}" --set ntfy.existingSecret=
expect_rejected "an ntfy URL with a scheme other than https or http" --values "${values}" --set ntfy.url=ftp://ntfy.sh
expect_rejected "a UI URL with a scheme other than https or http" --values "${values}" --set 'openbao.uiUrl=javascript:alert(1)'
expect_rejected "an invalid warning" --values "${values}" --set reminders.defaultExpiresWarnBefore=2weeks
expect_rejected "a token below the Kubernetes minimum" --values "${values}" --set openbao.auth.tokenExpirationSeconds=300
expect_rejected "an invalid concurrency policy" --values "${values}" --set concurrencyPolicy=Never
expect_rejected "an invalid digest" --values "${values}" --set image.digest=latest

echo "chart tests passed"
