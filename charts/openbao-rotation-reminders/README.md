# openbao-rotation-reminders chart

Deploys the
[OpenBao rotation reminders](https://github.com/mcsrobert/openbao-rotation-reminders)
as a `CronJob`. The project README describes the reminder metadata, the digest
and the OpenBao policy and role the job needs.

## Install

```sh
helm install openbao-rotation-reminders \
  oci://ghcr.io/mcsrobert/charts/openbao-rotation-reminders \
  --version <version> \
  --namespace security \
  --values values.yaml
```

A minimal `values.yaml`:

```yaml
---
openbao:
  address: https://openbao.security.svc.cluster.local:8200
  mount: homelab
ntfy:
  existingSecret: openbao-rotation-reminders-ntfy
```

Create the Secret first, with the private topic under the key `NTFY_TOPIC`:

```sh
kubectl --namespace security create secret generic \
  openbao-rotation-reminders-ntfy --from-literal=NTFY_TOPIC=<topic>
```

The job logs in with a projected service account token for the audience in
`openbao.auth.audience`, not the default service account token, which is not
mounted. The OpenBao Kubernetes auth role must use the same audience and bind
the release's service account and namespace. By default, the role and the
service account are both named after the release: `openbao-rotation-reminders`
for that release name, or `<release>-openbao-rotation-reminders` otherwise, cut
to 52 characters, the limit for a `CronJob` name. With
`serviceAccount.create=false`, set `serviceAccount.name` to the account the
role binds, because the job otherwise runs as `default`.

To run a check now:

```sh
kubectl --namespace security create job manual-check \
  --from=cronjob/openbao-rotation-reminders
kubectl --namespace security logs --follow job/manual-check
```

## Values

| Value | Default | Description |
| --- | --- | --- |
| `openbao.address` | required | OpenBao URL |
| `openbao.mount` | `homelab` | KV v2 mount |
| `openbao.uiUrl` | `""` | URL opened when the notification is tapped |
| `openbao.auth.role` | Full name of the release | Kubernetes auth role |
| `openbao.auth.audience` | `openbao` | Audience of the projected token |
| `openbao.auth.tokenExpirationSeconds` | `600` | Lifetime of the projected token, at least 600 |
| `openbao.tls.caSecretName` | `""` | Secret with the CA that signed OpenBao's certificate. Mounted at `/etc/openbao/ca.crt` |
| `openbao.tls.caSecretKey` | `ca.crt` | Key of the CA in that Secret |
| `ntfy.url` | `https://ntfy.sh` | ntfy server |
| `ntfy.existingSecret` | required | Secret with the topic |
| `ntfy.existingSecretKey` | `NTFY_TOPIC` | Key of the topic in that Secret |
| `reminders.defaultExpiresWarnBefore` | `14d` | Default lead time for `expires_at` |
| `schedule` | `0 9 * * *` | Cron schedule |
| `timeZone` | Controller's time zone | Time zone of the schedule, such as `Europe/Amsterdam` |
| `concurrencyPolicy` | `Forbid` | What to do when a run is still active |
| `startingDeadlineSeconds` | `300` | How late a missed run may start |
| `activeDeadlineSeconds` | `600` | Limit for one run |
| `backoffLimit` | `1` | Retries of a failed run |
| `successfulJobsHistoryLimit` | `3` | Finished jobs to keep |
| `failedJobsHistoryLimit` | `3` | Failed jobs to keep |
| `suspend` | `false` | Pause the schedule |
| `image.repository` | `ghcr.io/mcsrobert/openbao-rotation-reminders` | Image repository |
| `image.tag` | Chart's `appVersion` | Image tag |
| `image.digest` | The digest of the release | Image digest. Clear or replace it when you change the tag |
| `image.pullPolicy` | `IfNotPresent` | Pull policy |
| `imagePullSecrets` | `[]` | Pull secrets |
| `serviceAccount.create` | `true` | Create a service account |
| `serviceAccount.name` | Full name of the release, or `default` without `serviceAccount.create` | Service account name |
| `serviceAccount.annotations` | `{}` | Service account annotations |
| `networkPolicy.enabled` | `false` | Create a `NetworkPolicy` |
| `networkPolicy.openbao.podSelector` | OpenBao chart's server pods | OpenBao pods the job may reach |
| `networkPolicy.openbao.namespaceSelector` | `{}` | Namespaces of those pods. Empty means the release namespace |
| `networkPolicy.openbao.port` | `8200` | OpenBao port |
| `networkPolicy.extraEgress` | `[]` | More egress rules, for example to a self-hosted ntfy |
| `resources` | 10m CPU, 32Mi request, 64Mi limit | Container resources |
| `podSecurityContext` | Non-root, `RuntimeDefault` seccomp | Pod security context |
| `securityContext` | Read-only root, no capabilities | Container security context |
| `podAnnotations`, `podLabels` | `{}` | Extra pod metadata. `podLabels` may override the chart's labels except `app.kubernetes.io/name` and `app.kubernetes.io/instance`, which select the pods |
| `priorityClassName` | `""` | Priority class |
| `nodeSelector`, `tolerations`, `affinity` | Empty | Scheduling |
| `nameOverride`, `fullnameOverride` | `""` | Resource names |

## Network policy

With `networkPolicy.enabled`, the job's pods accept no ingress and may only
reach:

- DNS: `kube-dns` pods in `kube-system`, port 53 over UDP and TCP
- OpenBao: the selected pods on `networkPolicy.openbao.port` over TCP
- Public HTTPS: port 443 anywhere except private, carrier-grade NAT,
  link-local and loopback ranges, and IPv6 unique local and link-local
  addresses
- Anything in `networkPolicy.extraEgress`

The job's pods are labelled `app.kubernetes.io/name: openbao-rotation-reminders`,
so a policy on the OpenBao side can allow them in.
