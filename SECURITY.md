# Security Policy

## Supported versions

Promview is alpha. Only the most recent release receives fixes, and there are no
backports to earlier pre-releases.

| Version | Supported |
| --- | --- |
| Latest release | Yes |
| Anything earlier | No — upgrade |

See the [releases page](https://github.com/cropalato/promview/releases) for the
current version.

## Reporting a vulnerability

**Do not open a public issue.**

Report privately through GitHub:
[**Report a vulnerability**](https://github.com/cropalato/promview/security/advisories/new).
This opens a draft advisory visible only to you and the maintainer.

Useful to include, as far as you have it:

- the affected version, and whether the deployment runs in `open` or `oidc` mode
- what an attacker gains, and what access they need to start
- a reproduction — a request sequence is worth more than a description
- the deployment shape: Compose or Helm, and whether Promview is internet-facing

Expect an acknowledgement within a week. If a report is confirmed, the fix ships
in the next release and the advisory is published with credit, unless you ask
otherwise.

## Scope

In scope — anything reachable through Promview itself:

- **Authorization bypass.** Roles and label scopes are enforced in SQL. A
  principal listing, counting, streaming, or opening an alert outside its scope
  is a vulnerability, including through grouping, cursors, or the detail and
  history endpoints.
- **Authentication flaws.** The OIDC Authorization Code + PKCE flow, session
  issuance and revocation, the CSRF protection on browser mutations, and the
  desktop loopback code exchange.
- **Ingestion credentials.** Per-source bearer tokens are stored only as SHA-256
  hashes. Anything that recovers a token, lets one source write as another, or
  accepts an unauthenticated webhook is in scope.
- **Silence writes.** Creating or removing a silence is the only thing Promview
  writes to an Alertmanager. Reaching that without the matching per-alert
  permission is in scope.
- **Injection and data exposure** anywhere in the API, the console, or the
  desktop shell, including label values arriving from an Alertmanager.

Out of scope:

- Vulnerabilities in Alertmanager, Prometheus, or PostgreSQL. Report those
  upstream.
- Anything requiring database or host access — an attacker who is already there
  has won.
- Missing hardening headers with no demonstrated impact, and reports consisting
  only of automated scanner output.

## Known by design

These are documented behaviors rather than undisclosed weaknesses. Reports about
them are welcome as issues, but they are not vulnerabilities:

- **`alert_sources.alertmanager_token` is stored as given, not hashed.** It has
  to be replayed on every write to the Alertmanager. Treat that column as a
  secret at rest.
- **Alertmanager reads are unauthenticated.** Reconciliation reads
  `/api/v2/alerts` and `/api/v2/silences` without credentials, so a source behind
  authentication is not supported for reads yet.
- **`open` mode is anonymous and read-only by design.** Every reader is the same
  principal and all mutations are denied. Ingestion stays authenticated.
- **Desktop installers are unsigned.** Signing needs certificates this project
  does not have, and each release says so.

## Operator notes

- Serve Promview over TLS. OIDC issuer and redirect URLs must be HTTPS, and
  `PROMVIEW_SESSION_COOKIE_SECURE=false` is accepted only for loopback testing.
- **`PROMVIEW_OPEN_MODE_ROLE` above `viewer` grants that role to everyone who can
  reach the port, with no sign-in.** It exists for labs. Nothing done under it
  can be attributed to a person, and `administrator` lets any reader rewrite who
  has access, with bindings that outlive the lab. Alert on
  `promview_open_mode_elevated == 1` across the fleet: a startup warning is not
  something anybody watches, and this is the setting that rides into production
  unnoticed.
- Rotate a source token with `promview source set`; bootstrap configuration will
  not overwrite a rotated credential.
- Create at least one administrator binding before the first OIDC login.
  Unbound identities are denied by default.
- Promview runs as UID/GID 65532 and holds no state on disk. Give it a
  PostgreSQL role scoped to its own database.
