# Configure Prometheus And Alertmanager

Promview receives Prometheus alerts through an Alertmanager webhook. Prometheus does not send alerts directly to Promview.

The delivery path is:

```text
Prometheus alert rules -> Alertmanager -> authenticated Promview webhook
```

Alertmanager remains responsible for grouping, routing, inhibition, silences, and notification timing. Promview retains the current alert state, delivery history, and live console events.

## Prerequisites

- A running Promview deployment reachable from Alertmanager.
- A running Prometheus and Alertmanager deployment.
- Access to update the Prometheus and Alertmanager configuration files.
- `promtool` and `amtool` for configuration validation, or equivalent container commands.

The examples use:

```text
Promview URL: https://promview.example.com
Promview source: production
Alertmanager URL from Prometheus: alertmanager:9093
```

## Provision A Promview Source

Create a source for each independent Alertmanager deployment. Use a stable lowercase slug and a unique random token of at least 16 characters.

With the Promview Docker Compose deployment:

```sh
docker compose run --rm app source set \
  --slug production \
  --name 'Production Alertmanager' \
  --token 'replace-with-a-long-random-token'
```

Running the command again with the same slug rotates the source token. Promview stores only the SHA-256 token hash.

To change a source's settings later without touching its token, use `source update`:

```sh
docker compose run --rm app source update \
  --slug production \
  --alertmanager-url http://alertmanager:9093
```

The URL lets Promview read this Alertmanager's `/api/v2/alerts` and confirm what is
still firing, which is the only way it learns that a silenced alert ended: Alertmanager
sends no resolved notification for an alert that clears inside a silence. The API is
used read-only and unauthenticated. Without a URL, the source relies on expiry alone.

Store the original token in the secret manager used by Alertmanager. Do not commit it to either repository or place it directly in `alertmanager.yml`.

## Configure Prometheus

Configure Prometheus to send alerts to Alertmanager. Add or update the `alerting` and `rule_files` sections in `prometheus.yml`:

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

rule_files:
  - /etc/prometheus/rules/*.yml

alerting:
  alertmanagers:
    - static_configs:
        - targets:
            - alertmanager:9093
```

Use a DNS name and port reachable from the Prometheus process. In Kubernetes this is normally an Alertmanager Service name. In Docker Compose it is normally the Alertmanager service name.

If Alertmanager uses HTTPS, configure the scheme and TLS settings:

```yaml
alerting:
  alertmanagers:
    - scheme: https
      static_configs:
        - targets:
            - alertmanager.example.com
      tls_config:
        ca_file: /etc/prometheus/tls/internal-ca.pem
        server_name: alertmanager.example.com
```

Do not disable TLS certificate verification in production.

## Add An Alert Rule

Promview does not require a specific label schema, but `alertname`, `severity`, and `team` make the console and filters more useful.

Create a rule file such as `/etc/prometheus/rules/promview-example.yml`:

```yaml
groups:
  - name: promview-example
    rules:
      - alert: PromviewDeliveryTest
        expr: vector(1)
        for: 1m
        labels:
          severity: warning
          team: platform
        annotations:
          summary: Promview delivery test is firing
          description: Remove this rule after validating the integration.
```

This rule intentionally fires after one minute. Remove it after the end-to-end test.

## Configure The Alertmanager Webhook

Write the Promview source token to a file readable by Alertmanager:

```text
/etc/alertmanager/secrets/promview-production-token
```

The file must contain only the token. Restrict its permissions and mount it through your deployment's secret mechanism.

For a new Alertmanager configuration where Promview is the only receiver, use:

```yaml
global:
  resolve_timeout: 5m

route:
  receiver: promview-production
  group_by:
    - alertname
    - cluster
    - service
  group_wait: 10s
  group_interval: 1m
  repeat_interval: 4h

receivers:
  - name: promview-production
    webhook_configs:
      - url: https://promview.example.com/api/v1/ingest/alertmanager/production
        send_resolved: true
        max_alerts: 0
        http_config:
          authorization:
            type: Bearer
            credentials_file: /etc/alertmanager/secrets/promview-production-token
```

The final URL segment, `production`, must exactly match the slug passed to `promview source set`. The token file must contain the token configured for that same source.

Keep `send_resolved: true`. Without resolved deliveries, Promview cannot promptly reflect alerts that stopped firing.

`max_alerts: 0` leaves webhook batches unlimited. Promview limits each HTTP request body to 4 MiB, so installations with exceptionally large alert groups should set an appropriate positive `max_alerts` value and tune Alertmanager grouping.

## Preserve Existing Notification Routes

Most Alertmanager installations already route alerts to email, chat, paging, or other receivers. Add Promview without removing those routes.

The following pattern sends every alert to Promview and then continues through existing routes:

```yaml
route:
  receiver: existing-default
  group_by:
    - alertname
    - cluster
    - service
  group_wait: 10s
  group_interval: 1m
  repeat_interval: 4h
  routes:
    - receiver: promview-production
      continue: true

    - receiver: pager
      matchers:
        - severity="critical"

    - receiver: existing-default

receivers:
  - name: promview-production
    webhook_configs:
      - url: https://promview.example.com/api/v1/ingest/alertmanager/production
        send_resolved: true
        http_config:
          authorization:
            type: Bearer
            credentials_file: /etc/alertmanager/secrets/promview-production-token

  - name: pager
    # Existing paging configuration remains here.

  - name: existing-default
    # Existing default notification configuration remains here.
```

The Promview catch-all route must appear before routes that may stop sibling evaluation, and it must set `continue: true`. Keep an explicit final default route if unmatched alerts must still reach the existing default receiver. Adapt this structure to your routing tree and test it before deployment; nested routes may require a Promview route at the appropriate subtree instead of at the root.

To send only selected alerts to Promview, add matchers instead of using a catch-all route:

```yaml
routes:
  - receiver: promview-production
    matchers:
      - team=~"platform|payments"
    continue: true
```

## Configure TLS

When Promview uses a certificate signed by a private certificate authority, mount the CA certificate into Alertmanager and extend the webhook HTTP configuration:

```yaml
http_config:
  authorization:
    type: Bearer
    credentials_file: /etc/alertmanager/secrets/promview-production-token
  tls_config:
    ca_file: /etc/alertmanager/tls/internal-ca.pem
    server_name: promview.example.com
```

Do not use `insecure_skip_verify: true` in production.

## Validate And Reload

Validate the Prometheus configuration and rules before reloading:

```sh
promtool check config /etc/prometheus/prometheus.yml
promtool check rules /etc/prometheus/rules/*.yml
```

Validate Alertmanager:

```sh
amtool check-config /etc/alertmanager/alertmanager.yml
```

Reload each service using your deployment manager. Prometheus and Alertmanager also accept `SIGHUP`. Alertmanager supports `POST /-/reload`; Prometheus supports that endpoint only when started with `--web.enable-lifecycle`.

For Docker Compose deployments, recreate or restart the affected services after updating mounted configuration or secrets:

```sh
docker compose restart prometheus alertmanager
```

Check both services after the reload:

```sh
curl --fail http://prometheus.example.com/-/ready
curl --fail http://alertmanager.example.com/-/ready
```

## Verify End To End

The example `PromviewDeliveryTest` rule should become pending, then firing after one minute.

Verify each stage:

1. Open the Prometheus **Alerts** page and confirm `PromviewDeliveryTest` is firing.
2. Open the Alertmanager UI and confirm it received the alert.
3. Open Promview and confirm the alert appears with source `production`.
4. Confirm the `severity=warning` and `team=platform` labels are present.
5. Remove or disable the test rule.
6. Confirm Alertmanager sends a resolved delivery and Promview updates the alert state.

You can submit a temporary alert directly to Alertmanager when testing the Alertmanager-to-Promview segment independently of Prometheus:

```sh
amtool \
  --alertmanager.url=http://localhost:9093 \
  alert add PromviewWebhookTest \
  severity=warning \
  team=platform \
  --annotation=summary='Alertmanager to Promview webhook test'
```

Expire or remove the temporary alert after testing. The exact `amtool` options can vary with the Alertmanager release; run `amtool alert add --help` for the installed version.

## Multiple Alertmanager Deployments

Provision a separate Promview source and token for every independent Alertmanager deployment:

```text
production-us
production-eu
staging
```

Each Alertmanager then uses its matching URL and token:

```text
https://promview.example.com/api/v1/ingest/alertmanager/production-us
https://promview.example.com/api/v1/ingest/alertmanager/production-eu
https://promview.example.com/api/v1/ingest/alertmanager/staging
```

Alert fingerprints are scoped by source, so identical fingerprints from different Alertmanager deployments do not collide.

## Rotate A Source Token

Generate a new random token, then update Promview:

```sh
docker compose run --rm app source set \
  --slug production \
  --name 'Production Alertmanager' \
  --token 'replace-with-the-new-long-random-token'
```

Update the Alertmanager secret file immediately and reload Alertmanager. Token rotation takes effect in Promview immediately; the old token stops working as soon as `source set` succeeds.

For a no-gap rotation, provision a temporary second source, move Alertmanager to it, then rotate and restore the original source. Promview currently supports one active token per source.

## Troubleshooting

### Alertmanager Receives HTTP 401

- Confirm the source slug in the URL matches the provisioned source.
- Confirm the credential file contains only the current token, without surrounding quotes.
- Confirm Alertmanager reloaded after the secret changed.
- Confirm the token was not rotated by another `promview source set` command.

### Alertmanager Cannot Connect To Promview

- Test DNS and network access from the Alertmanager container or host.
- Confirm the reverse proxy forwards `/api/v1/ingest/alertmanager/` to Promview.
- Confirm the certificate is valid for the hostname used in the webhook URL.
- Mount the correct private CA instead of disabling certificate verification.

### Prometheus Shows A Firing Alert But Alertmanager Does Not

- Query `GET /api/v1/alertmanagers` on Prometheus and confirm the expected Alertmanager appears under `activeAlertmanagers`.
- Confirm the `alerting.alertmanagers` target is reachable from Prometheus.
- Confirm the rule is firing rather than pending.
- Inspect Prometheus logs for notification delivery errors.

### Alertmanager Has The Alert But Promview Does Not

- Confirm the alert matches the route that selects the Promview receiver.
- Confirm the Promview route is not placed after a matching sibling route that stops evaluation.
- Inspect Alertmanager logs for webhook status codes and TLS errors.
- Confirm Promview readiness at `https://promview.example.com/health/ready`.

### Resolved Alerts Stay Firing In Promview

- Confirm the webhook has `send_resolved: true`.
- Wait for Alertmanager's configured resolve and grouping intervals.
- Confirm the resolved alert still routes to the same Promview receiver and source.

## References

- [Prometheus alerting configuration](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#alertmanager_config)
- [Prometheus alerting rules](https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/)
- [Alertmanager configuration](https://prometheus.io/docs/alerting/latest/configuration/)
- [Alertmanager webhook receiver](https://prometheus.io/docs/alerting/latest/configuration/#webhook_config)
