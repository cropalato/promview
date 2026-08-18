import type { KeyboardEvent } from 'react';
import type { AlertSort } from '../alerts/api';
import { FIXED_COLUMNS, nextSort } from '../alerts/columns';
import type { ColumnDefinition } from '../alerts/columns';
import { SEVERITY_LABELS } from '../alerts/severity';
import type { AlertSummary } from '../alerts/types';
import { EmptyState } from './EmptyState';
import { SeverityIcon, SortIcon } from './icons';

export { nextSort };

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
  /** Columns to render, in order. Defaults to the console's built-in set. */
  columns?: readonly ColumnDefinition[];
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
  columns = FIXED_COLUMNS,
}: AlertTableProps) {
  return (
    <div className="table-panel">
      <div className="table-scroll">
        <table className="alert-table">
          <caption>Active alerts ({alerts.length})</caption>
          <thead>
            <tr>
              {columns.map((column) => (
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
                <td colSpan={columns.length}>
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
                  columns={columns}
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
  column: ColumnDefinition;
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
  columns,
  selected = false,
  onSelect,
}: {
  alert: AlertSummary;
  columns: readonly ColumnDefinition[];
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
      {columns.map((column) => (
        <AlertCell key={column.id} alert={alert} column={column} />
      ))}
    </tr>
  );
}

/**
 * One cell. Severity and state keep their own markup because they encode state
 * in form as well as in text; every other column is text from the registry.
 */
function AlertCell({ alert, column }: { alert: AlertSummary; column: ColumnDefinition }) {
  const cell = column.cell(alert);
  const className = [cell.className ?? '', column.optional ? 'col-optional' : '']
    .filter((part) => part !== '')
    .join(' ');

  if (column.id === 'severity') {
    return (
      <td className={className === '' ? undefined : className}>
        <span className={`sev-tag sev-${alert.severity}`}>
          <SeverityIcon severity={alert.severity} />
          <span>{alert.severityLabel ?? SEVERITY_LABELS[alert.severity]}</span>
        </span>
      </td>
    );
  }
  if (column.id === 'state') {
    return (
      <td className={className === '' ? undefined : className}>
        <span className={`state-chip state-${alert.state}`}>{alert.state}</span>
      </td>
    );
  }
  return <td className={className === '' ? undefined : className}>{cell.text}</td>;
}
