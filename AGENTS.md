# AGENTS.md

A CronJob that scans OpenBao KV v2 `custom_metadata` and sends one ntfy digest
of the secrets that need rotation. The [README](./README.md) is the contract:
its metadata keys, configuration, digest format and exit statuses are public
API, so change them deliberately and keep the README, the chart and its tests
in step.

## Layout

```text
cmd/openbao-rotation-reminders/    Entry point
internal/app/                      One run: configuration, scan, publish
internal/config/                   Environment variable parsing and validation
internal/openbao/                  Login, revocation and KV v2 metadata client
internal/reminder/                 custom_metadata evaluation
internal/digest/                   Notification title, priority and body
internal/ntfy/                     ntfy publisher
internal/httpclient/               TLS and no-redirect HTTP clients
charts/openbao-rotation-reminders/ Helm chart
test/integration/                  Tests against a real OpenBao dev server
test/chart/                        Chart rendering tests
test/e2e/                          kind test of the chart with OpenBao and ntfy
examples/                          OpenBao policy
```

## Rules

- Never call a KV `data` path or otherwise read secret values. The client only
  needs the Kubernetes login, `auth/token/revoke-self` and metadata endpoints.
- Never put tokens, JWTs, OpenBao response bodies, the ntfy topic or the ntfy
  request URL in errors or output.
- Neither HTTP client may follow redirects.
- A new chart value also goes in the chart's `values.schema.json`, which
  rejects unknown fields.
- Do not bump dependency, image or action versions by hand. Renovate manages
  them.
- Do not add explanatory comments to workflows or configuration files. Keep
  functional ones, such as Renovate annotations and action version comments.

## Validation

Run `mise run setup` once per checkout.

```sh
mise run lint
mise run test
mise run test:integration
mise run test:chart
mise run test:image    # needs Docker
mise run test:e2e      # needs Docker
```

## Git

- Commit messages are conventional commits with a scope, such as
  `fix(reminder): accept lowercase time zones`. A hook rejects anything else.
- Changes reach `main` through a pull request, with the `ci` check passing.
- Commit types decide releases: `feat` and `fix` create one, so use another type
  for changes that do not affect the image or chart.
- Releases come from merging the pull request release-please keeps open. It
  bumps the version in `Chart.yaml` too. Publishing its GitHub release runs the
  release workflow, which builds the image and chart for that version and pins
  the image digest in the chart. Do not create tags or releases by hand.
- Do not edit `CHANGELOG.md`, `.release-please-manifest.json` or the versions
  in `Chart.yaml`. Release-please maintains them.
- If the release workflow fails after publishing part of a release, re-run
  the failed jobs. It reuses an image or chart that the same commit already
  published and refuses any other existing version.
- GHCR creates new packages as private, so the first release's anonymous
  verification fails. Make the image and chart packages public, then re-run
  the failed jobs.
