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

## Quick Start

```sh
kubectl create namespace promview
kubectl --namespace promview create secret generic promview-database \
  --from-literal=url='postgres://promview:password@postgres.example.com:5432/promview?sslmode=verify-full'

helm upgrade --install promview oci://ghcr.io/cropalato/charts/promview \
  --namespace promview \
  --version 0.1.0-alpha.10
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
