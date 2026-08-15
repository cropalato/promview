import type { KeyboardEvent } from 'react';
import { formatAge } from '../alerts/format';
import { SEVERITY_LABELS } from '../alerts/severity';
import type { AlertSummary } from '../alerts/types';
import { EmptyState } from './EmptyState';
import { SeverityIcon } from './icons';

/**
 * Default console columns from the project plan. `optional` columns collapse
 * first on narrow viewports.
 */
const COLUMNS: ReadonlyArray<{ id: string; label: string; optional: boolean }> = [
  { id: 'severity', label: 'Severity', optional: false },
  { id: 'state', label: 'State', optional: false },
  { id: 'alert', label: 'Alert', optional: false },
  { id: 'summary', label: 'Summary', optional: true },
  { id: 'team', label: 'Team', optional: true },
  { id: 'instance', label: 'Instance', optional: true },
  { id: 'source', label: 'Source', optional: false },
  { id: 'age', label: 'Age', optional: false },
  { id: 'assignee', label: 'Assignee', optional: true },
  { id: 'notes', label: 'Notes', optional: true },
];

/** Cursor-pagination status and controls rendered below the table. */
export interface AlertPagination {
  /** Rows currently loaded in the browser. */
  loaded: number;
  /** Server-side count of every alert matching the current query. */
  total: number;
  hasMore: boolean;
  loadingMore: boolean;
  /** Pagination failure; loaded rows stay put and the next page retries. */
  error: Error | null;
  onLoadMore: () => void;
}

interface AlertTableProps {
  alerts: readonly AlertSummary[];
  filterActive?: boolean;
  filterQuery?: string;
  onClearFilter?: () => void;
  pagination?: AlertPagination;
  /** Id of the alert shown in the detail drawer, when one is open. */
  selectedId?: string | null;
  /** Row activation (click or Enter) opens the alert detail view. */
  onSelect?: (alert: AlertSummary) => void;
}

/** Dense alert table. Renders real rows when given data; otherwise the
 *  explicit empty state occupies the table body. Rows are focusable and
 *  activate with click or Enter when `onSelect` is provided. */
export function AlertTable({
  alerts,
  filterActive = false,
  filterQuery = '',
  onClearFilter,
  pagination,
  selectedId = null,
  onSelect,
}: AlertTableProps) {
  return (
    <div className="table-panel">
      <div className="table-scroll">
        <table className="alert-table">
          <caption>Active alerts ({alerts.length})</caption>
          <thead>
            <tr>
              {COLUMNS.map((column) => (
                <th
                  key={column.id}
                  scope="col"
                  className={column.optional ? 'col-optional' : undefined}
                >
                  {column.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {alerts.length === 0 ? (
              <tr className="empty-row">
                <td colSpan={COLUMNS.length}>
                  <EmptyState
                    filterActive={filterActive}
                    query={filterQuery}
                    onClearFilter={onClearFilter}
                  />
                </td>
              </tr>
            ) : (
              alerts.map((alert) => (
                <AlertRow
                  key={alert.id}
                  alert={alert}
                  selected={alert.id === selectedId}
                  onSelect={onSelect}
                />
              ))
            )}
          </tbody>
        </table>
      </div>
      {pagination !== undefined ? <TableFoot pagination={pagination} /> : null}
    </div>
  );
}

function TableFoot({ pagination }: { pagination: AlertPagination }) {
  const { loaded, total, hasMore, loadingMore, error, onLoadMore } = pagination;
  return (
    <div className="table-foot">
      <p className="table-foot-count">
        Showing {loaded} of {total} firing alerts · the text filter matches loaded rows only
      </p>
      {error !== null ? (
        <p className="table-foot-error" role="alert">
          Could not load more alerts: {error.message}
        </p>
      ) : null}
      {hasMore ? (
        <button type="button" className="button" onClick={onLoadMore} disabled={loadingMore}>
          {loadingMore ? 'Loading…' : error !== null ? 'Retry next page' : 'Load more'}
        </button>
      ) : null}
    </div>
  );
}

function AlertRow({
  alert,
  selected = false,
  onSelect,
}: {
  alert: AlertSummary;
  selected?: boolean;
  onSelect?: (alert: AlertSummary) => void;
}) {
  const selectable = onSelect !== undefined;
  const className = [selectable ? 'row-selectable' : '', selected ? 'row-selected' : '']
    .filter((part) => part !== '')
    .join(' ');

  const handleKeyDown = (event: KeyboardEvent<HTMLTableRowElement>) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      onSelect?.(alert);
    }
  };

  return (
    <tr
      className={className === '' ? undefined : className}
      tabIndex={selectable ? 0 : undefined}
      aria-selected={selectable ? selected : undefined}
      data-alert-id={alert.id}
      onClick={() => onSelect?.(alert)}
      onKeyDown={handleKeyDown}
    >
      <td>
        <span className={`sev-tag sev-${alert.severity}`}>
          <SeverityIcon severity={alert.severity} />
          <span>{alert.severityLabel ?? SEVERITY_LABELS[alert.severity]}</span>
        </span>
      </td>
      <td>
        <span className={`state-chip state-${alert.state}`}>{alert.state}</span>
      </td>
      <td className="cell-name">{alert.name}</td>
      <td className="cell-summary col-optional">{alert.summary}</td>
      <td className="cell-mono col-optional">{alert.team ?? '—'}</td>
      <td className="cell-mono col-optional">{alert.instance ?? '—'}</td>
      <td className="cell-mono">{alert.source}</td>
      <td className="cell-mono">{formatAge(alert.startsAt)}</td>
      <td className="cell-mono col-optional">{alert.assignee ?? '—'}</td>
      <td className="cell-mono col-optional">{alert.notes > 0 ? alert.notes : '—'}</td>
    </tr>
  );
}
