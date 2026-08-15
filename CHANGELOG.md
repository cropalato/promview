# Changelog

All notable changes to this project will be documented in this file.

The project uses [Conventional Commits](https://www.conventionalcommits.org/) and follows [Semantic Versioning](https://semver.org/). Entries are grouped by the conventional commit type that affects users or maintainers.

## [Unreleased]

### Features

- **api:** ingest and normalize authenticated Prometheus Alertmanager webhooks from multiple sources.
- **api:** expose filtered cursor pagination, severity counts, alert details, raw payloads, and immutable occurrence history.
- **stream:** publish durable, resumable server-sent events for created, changed, resolved, and reopened alerts.
- **web:** provide a responsive live alert console with filtering, pagination, connection state, deep-linked details, lifecycle timeline, and raw payload views.

### Build System

- **container:** build a non-root multi-stage application image and Docker Compose development stack.
- **database:** apply ordered PostgreSQL migrations before application startup and validate up/down migration paths.
- **ci:** run backend, frontend, migration, Compose, and container verification in GitHub Actions.

### Documentation

- Document the Alerta reference investigation, Promview architecture, implementation roadmap, desktop client direction, and developer workflows.
