import { RadarIcon } from './icons';

interface EmptyStateProps {
  filterActive: boolean;
  query?: string;
  onClearFilter?: () => void;
}

/**
 * No-alert state. The unfiltered variant doubles as operator guidance: it
 * documents the real ingestion endpoint the Go server already exposes.
 */
export function EmptyState({ filterActive, query = '', onClearFilter }: EmptyStateProps) {
  if (filterActive) {
    return (
      <div className="empty-state">
        <RadarIcon className="empty-icon" />
        <h2 className="empty-title">No alerts match the current filter</h2>
        <p className="empty-copy">
          Nothing matches <code>{query}</code>. Adjust or clear the label filter to widen the view.
        </p>
        {onClearFilter !== undefined ? (
          <button type="button" className="button empty-clear" onClick={onClearFilter}>
            Clear filter
          </button>
        ) : null}
      </div>
    );
  }

  return (
    <div className="empty-state">
      <RadarIcon className="empty-icon" />
      <h2 className="empty-title">All clear — no active alerts</h2>
      <p className="empty-copy">
        Promview has not received any alerts yet. Alerts delivered by connected Alertmanager sources
        will stream into this view as they fire.
      </p>
      <p className="empty-hint">POST /api/v1/ingest/alertmanager/&lbrace;source&rbrace;</p>
    </div>
  );
}
