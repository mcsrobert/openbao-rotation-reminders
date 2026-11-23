#!/usr/bin/env bash
set -euo pipefail

image=openbao-rotation-reminders:smoke

docker build --build-arg VERSION=smoke --tag "${image}" .

if [[ "$(docker run --rm "${image}" --version)" != smoke ]]; then
  echo "unexpected version" >&2
  exit 1
fi

if [[ "$(docker inspect --format '{{.Config.User}}' "${image}")" != 65532:65532 ]]; then
  echo "image does not run as the nonroot user by default" >&2
  exit 1
fi

directory=$(mktemp -d)
container=
cleanup() {
  if [[ -n "${container}" ]]; then
    docker rm --force "${container}" > /dev/null 2>&1 || true
  fi
  rm -rf "${directory}"
}
trap cleanup EXIT

container=$(docker create "${image}")
docker cp "${container}:/etc/ssl/certs/ca-certificates.crt" "${directory}/ca-certificates.crt"
if ! grep --quiet 'BEGIN CERTIFICATE' "${directory}/ca-certificates.crt"; then
  echo "the image has no CA certificates to verify ntfy" >&2
  exit 1
fi
docker rm "${container}" > /dev/null
container=

hardened() {
  docker run --rm \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --network none \
    "$@"
}

expect_failure() {
  local expected=$1
  shift
  local output status=0
  output=$(hardened "$@" 2>&1) || status=$?
  if (( status != 1 )) || [[ "${output}" != "${expected}" ]]; then
    echo "expected exit status 1 and '${expected}', got ${status}:" >&2
    echo "${output}" >&2
    exit 1
  fi
}

expect_failure \
  "configuration error: OPENBAO_ADDR: required; OPENBAO_MOUNT: required; OPENBAO_K8S_ROLE: required; NTFY_TOPIC must be 1 to 64 letters, digits, hyphens or underscores" \
  "${image}"

echo "smoke-test-jwt" > "${directory}/token"
chmod 755 "${directory}"
chmod 644 "${directory}/token"
expect_failure \
  "openbao error: request failed calling POST /v1/auth/kubernetes/login: dial tcp 127.0.0.1:1: connect: connection refused" \
  --volume "${directory}:/var/run/secrets/openbao:ro" \
  --env OPENBAO_ADDR=http://127.0.0.1:1 \
  --env OPENBAO_MOUNT=secret \
  --env OPENBAO_K8S_ROLE=smoke \
  --env NTFY_TOPIC=smoke-topic \
  "${image}"

echo "image smoke test passed"
