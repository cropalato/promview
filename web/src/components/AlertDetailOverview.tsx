import { safeExternalUrl } from '../alerts/detail';
import type { AlertDetail } from '../alerts/detail';
import { formatTimestamp } from '../alerts/format';
import { CopyButton } from './CopyButton';
import { ExternalLinkIcon, SeverityIcon } from './icons';

interface AlertDetailOverviewProps {
  detail: AlertDetail;
}

function byKey([a]: [string, string], [b]: [string, string]): number {
  return a.localeCompare(b);
}

/**
 * Overview tab of the alert detail drawer: the operational facts (severity,
 * state, source, occurrence, repeat count, timestamps), safe links out to
 * the generator/Alertmanager, and the full label/annotation sets with copy
 * controls. Read-only; open mode never offers mutating actions.
 */
export function AlertDetailOverview({ detail }: AlertDetailOverviewProps) {
  const labels = Object.entries(detail.labels).sort(byKey);
  const annotations = Object.entries(detail.annotations).sort(byKey);

  return (
    <div className="detail-overview">
      <dl className="detail-facts">
        <div className="detail-fact">
          <dt>Severity</dt>
          <dd>
            <span className={`sev-tag sev-${detail.severity}`}>
              <SeverityIcon severity={detail.severity} />
              <span>{detail.severityLabel}</span>
            </span>
          </dd>
        </div>
        <div className="detail-fact">
          <dt>State</dt>
          <dd>
            <span className={`state-chip state-${detail.status}`}>{detail.status}</span>
          </dd>
        </div>
        <div className="detail-fact">
          <dt>Source</dt>
          <dd className="detail-mono">{detail.source}</dd>
        </div>
        <div className="detail-fact">
          <dt>Occurrence</dt>
          <dd className="detail-mono">{detail.occurrence}</dd>
        </div>
        <div className="detail-fact">
          <dt>Repeat count</dt>
          <dd className="detail-mono">{detail.repeatCount}</dd>
        </div>
        <div className="detail-fact">
          <dt>Fingerprint</dt>
          <dd className="detail-mono detail-break">{detail.fingerprint || '—'}</dd>
        </div>
      </dl>

      <section className="detail-section" aria-label="Timestamps">
        <h3 className="detail-section-title">Timestamps</h3>
        <dl className="detail-times">
          <div className="detail-time">
            <dt>Started</dt>
            <dd className="detail-mono">{formatTimestamp(detail.startsAt)}</dd>
          </div>
          <div className="detail-time">
            <dt>Ended</dt>
            <dd className="detail-mono">
              {detail.endsAt !== null ? formatTimestamp(detail.endsAt) : 'Ongoing'}
            </dd>
          </div>
          <div className="detail-time">
            <dt>First seen</dt>
            <dd className="detail-mono">{formatTimestamp(detail.firstSeen)}</dd>
          </div>
          <div className="detail-time">
            <dt>Last seen</dt>
            <dd className="detail-mono">{formatTimestamp(detail.lastSeen)}</dd>
          </div>
        </dl>
      </section>

      <section className="detail-section" aria-label="External references">
        <h3 className="detail-section-title">Links</h3>
        <dl className="detail-times">
          <ExternalRef label="Generator URL" value={detail.generatorURL} />
          <ExternalRef label="Alertmanager URL" value={detail.externalURL} />
        </dl>
      </section>

      <section className="detail-section" aria-label="Labels">
        <h3 className="detail-section-title">Labels</h3>
        {labels.length === 0 ? (
          <p className="detail-empty-note">No labels.</p>
        ) : (
          <ul className="kv-list">
            {labels.map(([key, value]) => (
              <li key={key} className="kv-row">
                <span className="kv-key">{key}</span>
                <span className="kv-value">{value}</span>
                <CopyButton value={value} label={`Copy ${key}`} />
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="detail-section" aria-label="Annotations">
        <h3 className="detail-section-title">Annotations</h3>
        {annotations.length === 0 ? (
          <p className="detail-empty-note">No annotations.</p>
        ) : (
          <ul className="kv-list">
            {annotations.map(([key, value]) => (
              <li key={key} className="kv-row">
                <span className="kv-key">{key}</span>
                <span className="kv-value">{value}</span>
                <CopyButton value={value} label={`Copy ${key}`} />
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

/**
 * One external reference row. Only http/https URLs render as links (opened in
 * a new tab with `noopener`); anything else stays plain text so crafted
 * payloads cannot turn into javascript: links.
 */
function ExternalRef({ label, value }: { label: string; value: string }) {
  const safe = safeExternalUrl(value);
  return (
    <div className="detail-time">
      <dt>{label}</dt>
      <dd>
        {value.trim() === '' ? (
          <span className="detail-mono">—</span>
        ) : safe === null ? (
          <span className="detail-mono detail-break">{value}</span>
        ) : (
          <a className="detail-link" href={safe} target="_blank" rel="noopener noreferrer external">
            <ExternalLinkIcon className="detail-link-icon" />
            <span className="detail-break">{value}</span>
          </a>
        )}
      </dd>
    </div>
  );
}
