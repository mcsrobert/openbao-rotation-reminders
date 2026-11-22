# OpenBao rotation reminders

A Kubernetes `CronJob` that sends one daily [ntfy](https://ntfy.sh) digest of
the OpenBao KV v2 secrets that are expired, due for rotation, expiring soon or
have invalid reminder metadata.

Each secret declares its own reminder in its KV v2 `custom_metadata`, so you
can add or change one in the OpenBao web UI or with `bao kv metadata patch`,
without a new secret version or a change to this deployment. The job walks
the mount's metadata, never reads secret values and does not rotate anything.

> [!IMPORTANT]
> `rotation_interval` counts from the creation of the current KV version. **A
> new version resets the reminder, but it does not prove the credential was
> rotated.** Any write resets it, including one that rewrites the same value.
> Metadata-only changes, such as editing `custom_metadata`, do not.

## Reminder metadata

Set these keys in a secret's `custom_metadata`:

| Key | Value |
| --- | --- |
| `rotation_interval` | Positive duration, such as `90d`, measured from the current version's `created_time` |
| `expires_at` | Fixed expiry: `YYYY-MM-DD` (midnight UTC) or an RFC 3339 timestamp with a time zone offset |
| `rotation_warn_before` | Optional lead time, `0` allowed. Defaults to `DEFAULT_EXPIRES_WARN_BEFORE` (`14d`) for `expires_at` and `0` for `rotation_interval` |
| `rotation_note` | Optional instructions shown in the digest. Whitespace is collapsed |

Durations are a whole number followed by `h` (hours), `d` (days) or `w`
(weeks).

```sh
bao kv metadata patch -mount=homelab \
  -custom-metadata=rotation_interval=80d \
  -custom-metadata="rotation_note=Regenerate in the Tailscale admin console" \
  tailscale/auth-key

bao kv metadata patch -mount=homelab \
  -custom-metadata=expires_at=2027-03-01 \
  -custom-metadata=rotation_warn_before=30d \
  github/token
```

A secret is tracked when it has at least one key that starts with `rotation_`
or `expire`, ignoring case. Typos therefore show up instead of silently
disabling a reminder.

A secret is skipped entirely when its current version is soft-deleted (its
`deletion_time` has passed), destroyed, or when it has no version yet. A version
with a future deletion time, from `delete_version_after`, still counts.

## Findings

| Severity | When | Digest section | ntfy priority and tags |
| --- | --- | --- | --- |
| Expired | `expires_at` has passed | `EXPIRED` | `urgent`, `rotating_light,key` |
| Due | `created_time` plus `rotation_interval` has passed | `DUE FOR ROTATION` | `high`, `warning,key` |
| Upcoming | Within `rotation_warn_before` of either deadline | `EXPIRING SOON` | `default`, `key` |
| Invalid | Invalid metadata and nothing more urgent | `INVALID METADATA` | `default`, `key` |

Metadata is invalid when it has an unknown `rotation_*` or `expire*` key, a
malformed value, `rotation_warn_before` or `rotation_note` without a deadline,
or an active current version without a `created_time`. When a secret is also
expired, due or upcoming, the more urgent severity wins and the problem is
added to its reasons as `metadata error: ...`.

One run sends at most one notification. Its priority and tags follow the most
urgent finding. Findings are grouped by severity and sorted by deadline, then
key:

```text
OpenBao: 1 expired, 1 due, 1 invalid

EXPIRED:
- github/token: expired 2027-03-01 (2d ago)

DUE FOR ROTATION:
- tailscale/auth-key: due 2027-02-20 (11d overdue, interval 80d)
  Note: Regenerate in the Tailscale admin console

INVALID METADATA:
- app/database: metadata error: unknown metadata key "rotation_intervall"
```

The title names the secret when there is one finding, and counts per severity
otherwise. A body over 4096 bytes, ntfy's message limit, is cut to whole lines
and points to the job's log, which always has the full list.

## Configuration

The job reads environment variables. Unset and empty variables are the same.

| Variable | Default | Description |
| --- | --- | --- |
| `OPENBAO_ADDR` | required | OpenBao URL, `http` or `https` |
| `OPENBAO_MOUNT` | required | KV v2 mount |
| `OPENBAO_K8S_ROLE` | required | Kubernetes auth role |
| `OPENBAO_CA_FILE` | system roots | PEM file of CAs to trust for OpenBao |
| `OPENBAO_JWT_FILE` | `/var/run/secrets/openbao/token` | Service account token to log in with |
| `OPENBAO_UI_URL` | | Opened when the notification is tapped |
| `NTFY_URL` | `https://ntfy.sh` | ntfy server |
| `NTFY_TOPIC` | required | Private topic, without `/`, `?`, `&`, `#`, spaces, tabs or line breaks |
| `DEFAULT_EXPIRES_WARN_BEFORE` | `14d` | Default lead time for `expires_at`, `0d` allowed |

Anyone who knows the topic can read and publish to it, so treat it as a
secret. It never appears in output.

## Behavior

The job logs in with Kubernetes auth (`auth/kubernetes/login`), lists the
mount's metadata recursively, reads each secret's metadata, revokes its token
through `auth/token/revoke-self`, and publishes the digest if there are
findings. It prints `scanned=<keys> tracked=<with reminders> findings=<count>`
and the digest to stdout.

| Exit status | Meaning |
| --- | --- |
| `0` | No findings, or the digest was published |
| `1` | `configuration error`, `openbao error` or `notification error` on stderr |

Use your cluster's failed-job alerts, such as `KubeJobFailed`, to notice a job
that cannot run. Revocation is best effort: a failure prints a warning and does
not change the exit status. Batch tokens cannot be revoked and are left to
expire.

Neither client follows HTTP redirects, so a redirect cannot send the OpenBao
token or the ntfy topic to another host. Both require TLS 1.2 or later. Errors
never contain OpenBao response bodies, the ntfy URL or the topic.

## OpenBao access

The job needs to list and read metadata and to revoke its own token. Replace
`MOUNT` in [`examples/policy.hcl`](./examples/policy.hcl) with your mount:

```hcl
path "MOUNT/metadata/*" {
  capabilities = ["list", "read"]
}

path "auth/token/revoke-self" {
  capabilities = ["update"]
}
```

Bind a Kubernetes auth role to the chart's service account and namespace, with
the same audience as the projected token and a short TTL:

```sh
bao policy write openbao-rotation-reminders policy.hcl
bao write auth/kubernetes/role/openbao-rotation-reminders \
  bound_service_account_names=openbao-rotation-reminders \
  bound_service_account_namespaces=security \
  audience=openbao \
  token_ttl=5m \
  token_max_ttl=5m \
  token_policies=openbao-rotation-reminders \
  token_no_default_policy=true
```

The digest names secrets and includes their notes, so anyone who can read the
topic sees them.

## Deployment

Install the Helm chart from GHCR:

```sh
helm install openbao-rotation-reminders \
  oci://ghcr.io/mcsrobert/charts/openbao-rotation-reminders \
  --version <version> \
  --namespace security \
  --values values.yaml
```

The [chart README](./charts/openbao-rotation-reminders/README.md) lists its
values.

Each release publishes an image and a chart with the same version. Both are for
`linux/amd64` and `linux/arm64` hosts, and version tags are never overwritten.
The chart pins the image by digest. The image runs as a non-root user and works
with a read-only root filesystem and no capabilities.

Verify the build provenance of either one:

```sh
gh attestation verify \
  oci://ghcr.io/mcsrobert/openbao-rotation-reminders:<version> \
  --repo mcsrobert/openbao-rotation-reminders
gh attestation verify \
  oci://ghcr.io/mcsrobert/charts/openbao-rotation-reminders:<version> \
  --repo mcsrobert/openbao-rotation-reminders
```

## Development

```sh
mise run setup
mise run lint
mise run test
mise run test:integration
mise run test:chart
```

`mise run test:image` and `mise run test:e2e` also need Docker.

## License

[Apache-2.0](./LICENSE)
