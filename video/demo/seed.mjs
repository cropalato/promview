#!/usr/bin/env node
// Injects the demo alerts through each Alertmanager's own API, so Promview
// receives them the way it would in production: grouped, authenticated, and
// re-sent on the repeat interval.
//
//   node video/demo/seed.mjs --wave base        the fifteen alerts of the README screenshot
//   node video/demo/seed.mjs --wave staging     the staging source's alerts, for the multi-source scene
//   node video/demo/seed.mjs --wave incident    one new critical, for the notification scene
//   node video/demo/seed.mjs --wave all
//   node video/demo/seed.mjs --resolve HighMemoryUsage
//   node video/demo/seed.mjs --wave incident --fresh    a never-seen fingerprint, for a retake
//
// This file is the only place alert content lives. Scenes and captions read
// from it rather than restating it.

const PRODUCTION = process.env.DEMO_ALERTMANAGER_PRODUCTION ?? 'http://localhost:9093';
const STAGING = process.env.DEMO_ALERTMANAGER_STAGING ?? 'http://localhost:9094';

const minutes = (n) => n * 60_000;
const hours = (n) => minutes(60 * n);
const days = (n) => hours(24 * n);

// `age` is how long the alert has been firing when the scene starts, so the
// Age column reads like a real shift rather than a wall of "1m".
const alert = (source, age, labels, annotations) => ({ source, age, labels, annotations });

const production = [
  alert(
    'production',
    hours(7),
    {
      alertname: 'BackupJobFailed',
      severity: 'critical',
      team: 'data',
      namespace: 'backups',
      cluster: 'prod-eu1',
      environment: 'production',
      job: 'orders-db-nightly',
    },
    {
      summary: 'Nightly backup of orders-db failed',
      description:
        'Job orders-db-nightly exited 1: pg_dump: error: connection to server at "orders-db-primary" failed. Last successful backup was 31 hours ago.',
      runbook_url: 'https://runbooks.example.com/data/backup-job-failed',
    },
  ),
  alert(
    'production',
    hours(2),
    {
      alertname: 'TargetDown',
      severity: 'critical',
      team: 'infra',
      instance: 'node-14:9100',
      job: 'node-exporter',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'node-exporter on node-14 has been down for 2 hours',
      description: '100% of the node-exporter targets on node-14 are down.',
      runbook_url: 'https://runbooks.example.com/infra/target-down',
    },
  ),
  alert(
    'production',
    minutes(9),
    {
      alertname: 'PostgresReplicationLag',
      severity: 'critical',
      team: 'data',
      instance: 'db-replica-02:9187',
      cluster: 'prod-eu1',
      environment: 'production',
      database: 'orders',
    },
    {
      summary: 'Replica db-replica-02 is 412s behind the primary',
      description:
        'Replication lag on db-replica-02 has exceeded 300s for 5 minutes. Reads served from this replica are stale.',
      runbook_url: 'https://runbooks.example.com/data/replication-lag',
    },
  ),
  alert(
    'production',
    minutes(24),
    {
      alertname: 'KubePodCrashLooping',
      severity: 'critical',
      team: 'payments',
      namespace: 'payments',
      pod: 'checkout-api-7d9f4c8b6-x2k9p',
      container: 'checkout-api',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'Pod checkout-api-7d9f4c8b6-x2k9p is crash looping',
      description:
        'Container checkout-api has restarted 14 times in the last 30 minutes. Last exit: OOMKilled.',
      runbook_url: 'https://runbooks.example.com/kubernetes/crash-looping',
    },
  ),
  alert(
    'production',
    minutes(19),
    {
      alertname: 'HighMemoryUsage',
      severity: 'warning',
      team: 'platform',
      namespace: 'ingest',
      container: 'ingest-worker',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'Container ingest-worker is using 91% of its memory limit',
      description:
        'Memory working set has been above 90% of the limit for 15 minutes. The next spike is an OOM kill.',
      runbook_url: 'https://runbooks.example.com/platform/memory',
    },
  ),
  alert(
    'production',
    days(2),
    {
      alertname: 'PrometheusTimeseriesCardinality',
      severity: 'warning',
      team: 'observability',
      job: 'kubelet',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'Scrape job kubelet is producing 2.4M series',
      description:
        'The kubelet scrape job has grown past 2M active series. Check for a label with unbounded values, usually a pod or container id.',
      runbook_url: 'https://runbooks.example.com/observability/cardinality',
    },
  ),
  alert(
    'production',
    hours(1),
    {
      alertname: 'KubeDeploymentReplicasMismatch',
      severity: 'warning',
      team: 'platform',
      namespace: 'search',
      deployment: 'search-indexer',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'Deployment search-indexer has 2 of 5 replicas available',
      description:
        'Deployment search-indexer has not matched its expected replica count for over an hour.',
      runbook_url: 'https://runbooks.example.com/kubernetes/replicas-mismatch',
    },
  ),
  alert(
    'production',
    hours(28),
    {
      alertname: 'CertificateExpiringSoon',
      severity: 'warning',
      team: 'security',
      host: 'api.example.com',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'TLS certificate for api.example.com expires in 6 days',
      description:
        'The certificate served on api.example.com:443 expires on 2026-10-02. Renewal has not run.',
      runbook_url: 'https://runbooks.example.com/security/certificate-expiry',
    },
  ),
  alert(
    'production',
    hours(3),
    {
      alertname: 'NodeDiskSpaceLow',
      severity: 'warning',
      team: 'infra',
      instance: 'node-09:9100',
      mountpoint: '/var/lib/docker',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'Filesystem /var/lib/docker on node-09 is 87% full',
      description: 'At the current growth rate the filesystem fills in 2 days.',
      runbook_url: 'https://runbooks.example.com/infra/disk-space',
    },
  ),
  alert(
    'production',
    hours(5),
    {
      alertname: 'NodeDiskSpaceLow',
      severity: 'warning',
      team: 'infra',
      instance: 'node-07:9100',
      mountpoint: '/',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'Filesystem / on node-07 is 84% full',
      description: 'At the current growth rate the filesystem fills in 4 days.',
      runbook_url: 'https://runbooks.example.com/infra/disk-space',
    },
  ),
  alert(
    'production',
    minutes(41),
    {
      alertname: 'KubeHPAMaxedOut',
      severity: 'warning',
      team: 'payments',
      namespace: 'payments',
      horizontalpodautoscaler: 'checkout-api',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'HPA checkout-api has been at its maximum of 12 replicas for 40 minutes',
      description:
        'The autoscaler cannot add capacity. Either raise maxReplicas or find what is consuming it.',
      runbook_url: 'https://runbooks.example.com/kubernetes/hpa-maxed-out',
    },
  ),
  alert(
    'production',
    minutes(52),
    {
      alertname: 'KubeJobCompletedLate',
      severity: 'info',
      team: 'platform',
      namespace: 'reports',
      job_name: 'daily-usage-report',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'Job daily-usage-report completed 48 minutes after its deadline',
      description:
        'The job finished successfully but later than its schedule allows. Informational.',
      runbook_url: 'https://runbooks.example.com/platform/late-job',
    },
  ),
];

const staging = [
  alert(
    'staging',
    hours(1),
    {
      alertname: 'APILatencyHigh',
      severity: 'warning',
      team: 'payments',
      namespace: 'payments',
      route: '/v1/refunds',
      cluster: 'stg-eu1',
      environment: 'staging',
    },
    {
      summary: 'p99 latency on /v1/refunds is 2.8s',
      description: 'The p99 of the refunds route has been above 2s for 30 minutes on staging.',
      runbook_url: 'https://runbooks.example.com/payments/latency',
    },
  ),
  alert(
    'staging',
    minutes(16),
    {
      alertname: 'KubeNodeNotReady',
      severity: 'warning',
      team: 'infra',
      instance: 'stg-node-03',
      cluster: 'stg-eu1',
      environment: 'staging',
    },
    {
      summary: 'Node stg-node-03 has been NotReady for 15 minutes',
      description: 'The kubelet on stg-node-03 stopped posting status. Pods are being rescheduled.',
      runbook_url: 'https://runbooks.example.com/kubernetes/node-not-ready',
    },
  ),
  alert(
    'staging',
    minutes(6),
    {
      alertname: 'NodeRebootScheduled',
      severity: 'info',
      team: 'infra',
      instance: 'stg-node-01',
      cluster: 'stg-eu1',
      environment: 'staging',
    },
    {
      summary: 'stg-node-01 will reboot at 02:00 UTC for a kernel update',
      description:
        'Unattended upgrades has staged a kernel and scheduled the reboot. Informational.',
      runbook_url: 'https://runbooks.example.com/infra/scheduled-reboot',
    },
  ),
];

// Fired on cue during the notification scene: a new critical, distinct from
// everything already on screen so the viewer can see it arrive.
const incident = [
  alert(
    'production',
    0,
    {
      alertname: 'PaymentGatewayErrorRate',
      severity: 'critical',
      team: 'payments',
      namespace: 'payments',
      service: 'gateway',
      cluster: 'prod-eu1',
      environment: 'production',
    },
    {
      summary: 'Payment gateway error rate is 12% over the last 5 minutes',
      description:
        '5xx responses from the payment gateway exceeded 5% for 5 minutes. Checkouts are failing.',
      runbook_url: 'https://runbooks.example.com/payments/gateway-errors',
    },
  ),
];

export const waves = { base: production, staging, incident };
export const all = [...production, ...staging, ...incident];

const endpoints = { production: PRODUCTION, staging: STAGING };

function toAlertmanager(a, now, resolved = false) {
  return {
    labels: a.labels,
    annotations: a.annotations,
    // A resolved alert must have started before it ended, even one whose
    // fixture age is zero.
    startsAt: new Date(now - Math.max(a.age, resolved ? minutes(1) : 0)).toISOString(),
    // A far endsAt keeps the alert firing in Alertmanager without a Prometheus
    // to re-evaluate it; resolving is an endsAt in the past.
    endsAt: new Date(resolved ? now - 1000 : now + days(1)).toISOString(),
    generatorURL: `http://prometheus.${a.labels.cluster}.example.com/graph?g0.expr=${encodeURIComponent(a.labels.alertname)}`,
  };
}

async function post(source, alerts) {
  const url = `${endpoints[source]}/api/v2/alerts`;
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(alerts),
  });
  if (!response.ok) {
    throw new Error(`${url}: ${response.status} ${await response.text()}`);
  }
}

async function send(list, resolved = false) {
  const now = Date.now();
  for (const source of Object.keys(endpoints)) {
    const batch = list
      .filter((a) => a.source === source)
      .map((a) => toAlertmanager(a, now, resolved));
    if (batch.length === 0) continue;
    await post(source, batch);
    console.log(`${resolved ? 'resolved' : 'fired'} ${batch.length} on ${source}`);
  }
}

async function main(argv) {
  const args = new Map();
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i].startsWith('--') && argv[i + 1] && !argv[i + 1].startsWith('--'))
      args.set(argv[i], argv[++i]);
    else args.set(argv[i], true);
  }

  if (args.has('--resolve')) {
    const name = args.get('--resolve');
    const matching = all.filter((a) => a.labels.alertname === name);
    if (matching.length === 0) throw new Error(`no fixture named ${name}`);
    return send(matching, true);
  }

  const wave = args.get('--wave') ?? 'base';
  let list = wave === 'all' ? all : waves[wave];
  if (!list) throw new Error(`unknown wave ${wave}; use ${Object.keys(waves).join(', ')} or all`);
  // Promview announces only alerts it has never seen. A retake on a stack
  // that already saw the wave gets a fresh fingerprint through one extra label.
  if (args.has('--fresh')) {
    const retake = `r${Date.now().toString(36)}`;
    list = list.map((a) => ({ ...a, labels: { ...a.labels, retake } }));
  }
  return send(list);
}

if (process.argv[1] && import.meta.url.endsWith(process.argv[1].split('/').pop())) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(error.message);
    process.exit(1);
  });
}
