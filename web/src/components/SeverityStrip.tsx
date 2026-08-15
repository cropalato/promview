import { SEVERITIES, SEVERITY_LABELS, normalizeSeverity } from '../alerts/severity';
import type { Severity } from '../alerts/severity';
import { SeverityIcon } from './icons';

interface SeverityStripProps {
  /** Server-side severity histogram for the current query. */
  counts: Record<string, number>;
  /** Server-side total of matching alerts; drives the proportions. */
  total: number;
}

/**
 * Compact severity summary strip. Counts come from the query API, so they
 * cover every alert matching the current query — not just the rows loaded
 * so far. Severities outside the three visual buckets fold into info,
 * matching how table rows render them. Each segment pairs a shape-coded
 * glyph with a count, label, and proportional bar so severity never relies
 * on color alone.
 */
export function SeverityStrip({ counts, total }: SeverityStripProps) {
  const bucketed: Record<Severity, number> = { critical: 0, warning: 0, info: 0 };
  for (const [name, count] of Object.entries(counts)) {
    if (Number.isFinite(count) && count > 0) {
      bucketed[normalizeSeverity(name)] += count;
    }
  }

  return (
    <section className="sev-strip" aria-label="Severity summary">
      <ul className="sev-list">
        {SEVERITIES.map((severity) => {
          const percent = total <= 0 ? 0 : Math.round((bucketed[severity] / total) * 100);
          return (
            <li key={severity} className={`sev sev-${severity}`}>
              <SeverityIcon severity={severity} className="sev-icon" />
              <span className="sev-count">{bucketed[severity]}</span>
              <span className="sev-label">{SEVERITY_LABELS[severity]}</span>
              <span className="sev-bar" aria-hidden="true">
                <span className="sev-bar-fill" style={{ width: `${percent}%` }} />
              </span>
            </li>
          );
        })}
      </ul>
      <p className="sev-meta">{total} firing</p>
    </section>
  );
}
