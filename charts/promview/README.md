# Promview Helm Chart

This chart installs Promview into Kubernetes and runs database migrations before each install or upgrade. PostgreSQL is an external prerequisite and is not managed by the chart.

## Prerequisites

- Kubernetes 1.25 or newer
- Helm 3.14 or newer
- PostgreSQL 17 or another supported PostgreSQL release
- A PostgreSQL role with schema migration permissions
- A Secret containing the PostgreSQL connection URL

Promview must be served at the origin root. URL prefixes such as `/promview` are not supported.

## Install

Create the namespace and database Secret first because the migration hook runs before normal chart resources:

```sh
kubectl create namespace promview
kubectl --namespace promview create secret generic promview-database \
  --from-literal=url='postgres://promview:password@postgres.example.com:5432/promview?sslmode=verify-full'
```

Install from the OCI registry after a release is published:

```sh
helm install promview oci://ghcr.io/cropalato/charts/promview \
  --namespace promview \
  --version 0.1.0-alpha.5
```

Install a local checkout:

```sh
helm install promview charts/promview --namespace promview
```

Forward the service for an open-mode test:

```sh
kubectl --namespace promview port-forward service/promview 8080:80
```

Then open <http://localhost:8080>.

## OIDC

Create the client-secret Secret:

```sh
kubectl --namespace promview create secret generic promview-oidc \
  --from-literal=client-secret='replace-with-provider-client-secret'
```

Create `oidc-values.yaml`:

```yaml
auth:
  mode: oidc

oidc:
  issuerURL: https://your-org.okta.com/oauth2/default
  clientID: promview
  existingSecret: promview-oidc
  redirectURL: https://promview.example.com/api/v1/auth/oidc/callback

ingress:
  enabled: true
  className: nginx
  annotations:
    nginx.ingress.kubernetes.io/proxy-buffering: "off"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-body-size: 5m
  hosts:
    - host: promview.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: promview-tls
      hosts:
        - promview.example.com
```

Apply it with:

```sh
helm upgrade --install promview oci://ghcr.io/cropalato/charts/promview \
  --namespace promview \
  --version 0.1.0-alpha.5 \
  --values oidc-values.yaml
```

Configure initial role bindings through Helm values before signing in:

```yaml
roleBindings:
  - name: promview-administrators
    role: administrator
    oidcIssuer: https://identity.example.com
    oidcGroup: promview-administrators
```

The chart applies each binding after install and upgrade. A binding with the same name is replaced atomically; bindings not declared in chart values are left unchanged. The chart requires `auth.mode: oidc` when `roleBindings` is set.

Alternatively, create an initial administrator binding manually:

```sh
kubectl --namespace promview exec deployment/promview -- \
  promview access set \
  --name promview-administrators \
  --role administrator \
  --oidc-issuer 'https://your-org.okta.com/oauth2/default' \
  --oidc-group 'promview-administrators'
```

The issuer must exactly match `oidc.issuerURL`. Use repeated `--selector` flags for scoped viewer or operator bindings.

See [`../../docs/authorization.md`](../../docs/authorization.md) for complete binding and selector semantics.

See [`../../docs/okta-oidc.md`](../../docs/okta-oidc.md) for Okta application and claim configuration.

## Bootstrap An Alertmanager Source

Create a source-token Secret:

```sh
kubectl --namespace promview create secret generic promview-source-production \
  --from-literal=token='replace-with-at-least-16-characters'
```

Enable bootstrap values:

```yaml
bootstrapSource:
  enabled: true
  slug: production
  name: Production Alertmanager
  existingSecret: promview-source-production
```

Bootstrap initializes an absent or legacy uncredentialed source. Changing the Secret does not rotate a source that already has a credential. Use `promview source set` explicitly for rotation.

## Migrations And Rollbacks

The chart runs `promview migrate` as a `pre-install,pre-upgrade` Helm hook using the same image as the Deployment. Migration failure blocks the release. Promview serializes migration processes with a PostgreSQL advisory lock.

The published image and default pod security context use the fixed non-root UID and GID `65532` for compatibility with restricted Kubernetes policies.

Helm rollback never executes down migrations. Schema changes must remain compatible with the previous application version during rolling replacement and rollback.

Do not run concurrent releases against the same database unless they use compatible Promview versions. Back up PostgreSQL before upgrades and manage database recovery outside this chart.

## Verify

```sh
helm test promview --namespace promview
kubectl --namespace promview get deployment,pod,service,ingress
```

Local chart verification:

```sh
make verify-helm
```

## Important Values

| Value | Default | Description |
| --- | --- | --- |
| `replicaCount` | `1` | Number of application replicas |
| `image.repository` | `cropalato/promview` | Application image repository |
| `image.tag` | chart app version | Application image tag |
| `image.digest` | empty | Optional immutable image digest; mutually exclusive with tag |
| `database.existingSecret` | `promview-database` | Secret containing the PostgreSQL URL |
| `database.urlKey` | `url` | PostgreSQL URL key |
| `auth.mode` | `open` | `open` or `oidc` |
| `oidc.existingSecret` | empty | Secret containing the OIDC client secret |
| `bootstrapSource.enabled` | `false` | Initialize one Alertmanager source |
| `roleBindings` | `[]` | OIDC group role bindings applied after install and upgrade |
| `migration.enabled` | `true` | Run migrations before install and upgrade |
| `ingress.enabled` | `false` | Create an Ingress |
| `podDisruptionBudget.enabled` | `false` | Create a PodDisruptionBudget |
| `helmTest.enabled` | `true` | Create the Helm health test pod |

See `values.yaml` for scheduling, probes, security contexts, resources, extra environment variables, and volume extension points.

`extraVolumes` and `extraVolumeMounts` apply to both the Deployment and migration Job. Use them to mount a private PostgreSQL CA certificate, then reference that path from the connection URL stored in the database Secret.
