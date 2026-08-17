# Configure Authorization

Promview separates OIDC authentication from authorization. The identity provider proves who the user is and supplies group membership. Server-owned database bindings decide which alerts that identity can read or operate on.

## Roles

| Role | Access |
| --- | --- |
| `viewer` | Read alerts and history matching the binding selectors |
| `operator` | Viewer access plus acknowledge and unacknowledge actions within the same selectors |
| `administrator` | Global access; administrator bindings cannot have selectors |

Open mode supplies an anonymous global viewer. OIDC mode denies identities without at least one readable binding.

## OIDC Group Bindings

Create a global administrator binding:

```sh
promview access set \
  --name promview-administrators \
  --role administrator \
  --oidc-issuer 'https://identity.example.com' \
  --oidc-group 'promview-administrators'
```

Create an operator restricted to production platform alerts:

```sh
promview access set \
  --name platform-production-operators \
  --role operator \
  --oidc-issuer 'https://identity.example.com' \
  --oidc-group 'promview-platform' \
  --selector 'team=platform' \
  --selector 'environment=production'
```

The issuer must exactly match the configured OIDC issuer and the validated ID-token issuer.

## Direct User Bindings

Bindings can target a persistent Promview user ID instead of an OIDC group:

```sh
promview access set \
  --name incident-commander \
  --role operator \
  --user-id 42 \
  --selector 'incident=true'
```

Group bindings are recommended for initial access because an identity must first sign in successfully before its persistent user ID exists.
An authenticated user can inspect their own ID through `GET /api/v1/me`.

## Selector Semantics

Supported operators are:

| Operator | Meaning |
| --- | --- |
| `=` | Label exists and equals the value |
| `!=` | Label exists and differs from the value |
| `=~` | Label exists and matches the regular expression |
| `!~` | Label exists and does not match the regular expression |

Selectors repeated within one binding are ANDed. Multiple bindings that apply to the same user are ORed. A viewer or operator binding without selectors is global.

Missing labels never satisfy a selector, including negative selectors. This prevents a broad negative selector from accidentally granting unlabeled alerts.

Examples:

```text
team=platform
severity=~critical|warning
environment!=development
region!~test-.*
```

## Update Or Delete A Binding

Running `access set` with an existing name replaces its subject, role, and complete selector list atomically:

```sh
promview access set \
  --name platform-production-operators \
  --role viewer \
  --oidc-issuer 'https://identity.example.com' \
  --oidc-group 'promview-platform' \
  --selector 'team=platform'
```

Delete a binding:

```sh
promview access delete --name platform-production-operators
```

Bindings and the most recently observed OIDC group memberships are evaluated for every authenticated request. Removing a binding invalidates access for existing sessions immediately. Identity-provider group changes take effect when Promview next observes them during a successful login. Long-lived streams re-check current bindings before delivering new events.

## Inspect OIDC Authorization

Administrators can inspect the persisted identities, their most recently observed groups, and configured bindings. The command emits JSON and never includes provider tokens or Promview session values:

```sh
promview access inspect
```

Use it only in a trusted administrative environment because identity and group information is sensitive.

## Scoped Streams

Stream events carry label snapshots from the time of the alert change. When an alert moves into a user's scope, Promview sends the normal event. When an alert moves out of scope, Promview sends a redacted `alert.removed` event containing only the event and alert IDs and timestamp, allowing the client to remove stale data without exposing the new labels or summary.

## Docker Compose

Run administration commands with the application service configuration:

```sh
docker compose run --rm app access set \
  --name platform-viewers \
  --role viewer \
  --oidc-issuer 'https://identity.example.com' \
  --oidc-group 'promview-platform' \
  --selector 'team=platform'
```

## Kubernetes

Execute the CLI in a running Promview pod so it reuses the database Secret:

```sh
kubectl --namespace promview exec deployment/promview -- \
  promview access set \
  --name platform-viewers \
  --role viewer \
  --oidc-issuer 'https://identity.example.com' \
  --oidc-group 'promview-platform' \
  --selector 'team=platform'
```

Treat binding changes as privileged administrative operations and record them through your deployment/change-management process until a dedicated administration API and audit UI are implemented.
