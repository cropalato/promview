# Changelog

All notable changes to this project will be documented in this file.

The project uses [Conventional Commits](https://www.conventionalcommits.org/) and follows [Semantic Versioning](https://semver.org/). Entries are grouped by the conventional commit type that affects users or maintainers.

## [Unreleased]

### Features

- **web:** add opt-in browser notifications for newly created critical alerts while Promview is open.

### Build System

- **helm:** add a hardened Kubernetes chart with migration hooks, external Secret integration, OIDC, Ingress, and health tests.
- **release:** publish multi-architecture images and the Helm chart to GHCR from version tags.

### Documentation

- Add Kubernetes installation, upgrade, rollback, OIDC, and production-operation guidance.

## [0.1.0-alpha.1] - 2026-08-15

### Features

- **api:** ingest and normalize authenticated Prometheus Alertmanager webhooks from multiple sources.
- **auth:** provision independent Alertmanager source credentials, store only token hashes, and track source deliveries.
- **auth:** protect query and stream APIs with open-mode or opaque-session authentication and expose the current principal.
- **auth:** add OIDC discovery and Authorization Code sign-in with PKCE, replay protection, validated ID tokens, group-to-role mapping, and logout.
- **api:** expose filtered cursor pagination, severity counts, alert details, raw payloads, and immutable occurrence history.
- **stream:** publish durable, resumable server-sent events for created, changed, resolved, and reopened alerts.
- **web:** provide a responsive live alert console with filtering, pagination, connection state, deep-linked details, lifecycle timeline, and raw payload views.
- **web:** gate protected alerts and streams on OIDC identity, expose sign-in and logout, and recover when sessions expire.

### Build System

- **container:** build a non-root multi-stage application image and Docker Compose development stack.
- **database:** apply ordered PostgreSQL migrations before application startup and validate up/down migration paths.
- **ci:** run backend, frontend, migration, Compose, and container verification in GitHub Actions.

### Documentation

- Document the Alerta reference investigation, Promview architecture, implementation roadmap, desktop client direction, and developer workflows.
- Add an Okta-specific OIDC application, claims, role-mapping, deployment, and troubleshooting guide.
- Add a Prometheus and Alertmanager configuration procedure for authenticated Promview webhook delivery.
