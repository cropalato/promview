# Install Promview On Kubernetes

The supported Kubernetes package is the Helm chart under [`charts/promview`](../charts/promview). It deploys the stateless Promview application and expects an externally managed PostgreSQL database.

## Architecture

The release contains:

- a pre-install and pre-upgrade migration Job
- a hardened non-root Deployment
- a ClusterIP Service
- an optional Ingress
- an optional PodDisruptionBudget
- a Helm health-test pod

Sessions, OIDC transactions, alerts, and resumable stream events are stored in PostgreSQL. Sticky sessions and application persistent volumes are not required.

## Production Checklist

- Use an immutable image digest or release tag.
- Store the database URL, OIDC client secret, and source tokens in Kubernetes Secrets.
- Use PostgreSQL TLS, backups, tested restores, and a role allowed to apply migrations.
- Terminate HTTPS at the Ingress and expose Promview at `/` without path rewriting.
- Disable Ingress buffering and allow long-lived connections for `/api/v1/stream`.
- Allow network egress to PostgreSQL and, in OIDC mode, provider discovery, token, and JWKS endpoints.
- Allow egress to each source's Alertmanager API if reconciliation is used; it is read-only and unauthenticated, so a source behind authentication is not supported yet.
- Start with one replica and measure PostgreSQL connection and SSE polling load before scaling.
- Configure CPU and memory requests and limits from observed workload data.
- Back up PostgreSQL before upgrades. Helm rollback does not reverse database migrations.

## Upgrading

The chart is packaged with its `version` and `appVersion` set to the same release
tag, and the Deployment resolves its image as `default .Chart.AppVersion
.Values.image.tag`. Unless a deployment overrides `image.tag`, **the chart version
it pins is the application version it runs.** Upgrading is therefore a single
change: move the pinned chart version.

Where that pin lives depends on how the chart is applied. A `helm upgrade` names it
with `--version`; a GitOps repository holding a `helmfile.yaml`, an Argo CD
`Application`, or a Flux `HelmRelease` holds it in the manifest. Publishing a
release does not reach a cluster on its own — whatever holds the pin has to be
bumped, and a pin left behind is why a cluster can sit several releases back
without anything reporting a problem.

Apply the upgrade through Helm, not by editing the running Deployment. The
migration Job is a `pre-install,pre-upgrade` hook, so Helm runs it to completion
before the Deployment rolls; changing the image on a live Deployment bypasses the
hook entirely, and the new binary then starts against the old schema. Promview
refuses to serve in that state rather than answering errors it cannot explain:

```
ERROR promview stopped error="database schema is behind this binary; run `promview migrate` first (unapplied: 000015_silence_provenance.up.sql)"
```

Recovering from that needs migrations run as their own workload, because the
application pod will not stay up long enough to exec into. Re-running the upgrade
through Helm is usually enough, since the hook Job is independent of the
Deployment:

```sh
helm upgrade promview oci://ghcr.io/cropalato/charts/promview \
  --namespace promview --reuse-values --version <release>
```

Name the version explicitly. `--reuse-values` alone reinstates the version the
release already recorded, which runs the migration Job on the *old* image and
leaves the schema exactly where it was.

The guard only refuses the serve path. `promview migrate`, `promview source`, and
`promview access` still run against a schema behind the binary, so the tools for
repairing one are never the thing it locks away.

## Quick Start

```sh
kubectl create namespace promview
kubectl --namespace promview create secret generic promview-database \
  --from-literal=url='postgres://promview:password@postgres.example.com:5432/promview?sslmode=verify-full'

helm upgrade --install promview oci://ghcr.io/cropalato/charts/promview \
  --namespace promview \
  --version 0.1.0-alpha.30
```

Alert staleness is configured through chart values, which render into the application
ConfigMap:

```yaml
alertExpiry:
  staleAfter: 12h   # "0" disables expiry; must exceed the source repeat_interval
  interval: 1m
reconcile:
  interval: 1m      # "0" disables reading Alertmanager
  timeout: 10s
```

Reconciliation stays inert until a source carries an Alertmanager URL, which is set
per source rather than through chart values:

```sh
kubectl --namespace promview exec deploy/promview -- \
  promview source update --slug <slug> --alertmanager-url http://alertmanager:9093
```

`source update` deliberately does not take a token, so adding a URL never rewrites the
credential the source authenticates its deliveries with. See
[`project-plan.md`](project-plan.md) for how expiry and reconciliation differ, and for
a known gap: an alert expiry retired is not revived even when the Alertmanager still
holds it.

The PostgreSQL Secret must exist before installation because the migration Job is a Helm pre-install hook.

For all chart values, OIDC configuration, source bootstrap, migration behavior, and validation commands, see [`charts/promview/README.md`](../charts/promview/README.md).

OIDC deployments must create at least one group binding after installation and before the first login. The chart guide includes the required `promview access set` command.

For Alertmanager webhook configuration, see [`prometheus-alertmanager.md`](prometheus-alertmanager.md).
