import type { KeyboardEvent } from 'react';
import type { AlertSort, AlertSortField } from '../alerts/api';
import { formatAge } from '../alerts/format';
import { SEVERITY_LABELS } from '../alerts/severity';
import type { AlertSummary } from '../alerts/types';
import { EmptyState } from './EmptyState';
import { SeverityIcon, SortIcon } from './icons';

/**
 * Default console columns from the project plan. `optional` columns collapse
 * first on narrow viewports. `sortField` marks columns the alerts endpoint
 * can sort server-side; the rest render plain headers.
 */
const COLUMNS: ReadonlyArray<{
  id: string;
  label: string;
  optional: boolean;
  sortField?: AlertSortField;
}> = [
  { id: 'severity', label: 'Severity', optional: false, sortField: 'severity' },
  { id: 'state', label: 'State', optional: false, sortField: 'state' },
  { id: 'alert', label: 'Alert', optional: false, sortField: 'name' },
  { id: 'summary', label: 'Summary', optional: true, sortField: 'summary' },
  { id: 'team', label: 'Team', optional: true, sortField: 'team' },
  { id: 'instance', label: 'Instance', optional: true, sortField: 'instance' },
  { id: 'source', label: 'Source', optional: false, sortField: 'source' },
  { id: 'age', label: 'Age', optional: false, sortField: 'age' },
  { id: 'assignee', label: 'Assignee', optional: true },
  { id: 'notes', label: 'Notes', optional: true },
];

/**
 * Next sort when a header is activated: an inactive column starts ascending,
 * the active column toggles direction.
 */
export function nextSort(field: AlertSortField, current: AlertSort | null): AlertSort {
  if (current !== null && current.field === field) {
    return { field, order: current.order === 'asc' ? 'desc' : 'asc' };
  }
  return { field, order: 'asc' };
}

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
  /** Active server-side sort; the matching header exposes it via aria-sort. */
  sort?: AlertSort | null;
  /** Header activation requests a server-side sort for that column. */
  onSortChange?: (sort: AlertSort) => void;
}

/** Dense alert table. Renders real rows when given data; otherwise the
 *  explicit empty state occupies the table body. Rows are focusable and
 *  activate with click or Enter when `onSelect` is provided. Sortable
 *  columns render an APG-style header button; the actively sorted column
 *  carries `aria-sort`. */
export function AlertTable({
  alerts,
  filterActive = false,
  filterQuery = '',
  onClearFilter,
  pagination,
  selectedId = null,
  onSelect,
  sort = null,
  onSortChange,
}: AlertTableProps) {
  return (
    <div className="table-panel">
      <div className="table-scroll">
        <table className="alert-table">
          <caption>Active alerts ({alerts.length})</caption>
          <thead>
            <tr>
              {COLUMNS.map((column) => (
                <ColumnHeader
                  key={column.id}
                  column={column}
                  sort={sort}
                  onSortChange={onSortChange}
                />
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

/**
 * One column header. Sortable columns render a button following the APG
 * sortable-table pattern: the sorted column's `<th>` carries `aria-sort`,
 * and the button name describes the action.
 */
function ColumnHeader({
  column,
  sort,
  onSortChange,
}: {
  column: (typeof COLUMNS)[number];
  sort: AlertSort | null;
  onSortChange?: (sort: AlertSort) => void;
}) {
  const sortField = column.sortField;
  if (sortField === undefined || onSortChange === undefined) {
    return (
      <th scope="col" className={column.optional ? 'col-optional' : undefined}>
        {column.label}
      </th>
    );
  }
  const active = sort !== null && sort.field === sortField;
  return (
    <th
      scope="col"
      className={column.optional ? 'col-optional' : undefined}
      aria-sort={active ? (sort.order === 'asc' ? 'ascending' : 'descending') : undefined}
    >
      <button
        type="button"
        className="th-sort"
        aria-label={`Sort by ${column.label}`}
        data-direction={active ? sort.order : 'none'}
        onClick={() => onSortChange(nextSort(sortField, sort))}
      >
        <span>{column.label}</span>
        <SortIcon className="th-sort-icon" />
      </button>
    </th>
  );
}

function TableFoot({ pagination }: { pagination: AlertPagination }) {
  const { loaded, total, hasMore, loadingMore, error, onLoadMore } = pagination;
  return (
    <div className="table-foot">
      <p className="table-foot-count">
        Showing {loaded} of {total} firing alerts
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
