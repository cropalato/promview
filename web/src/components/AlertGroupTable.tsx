import type { KeyboardEvent } from 'react';
import type { AlertGroupSummary, AlertSort } from '../alerts/api';
import { FIXED_COLUMNS, columnWidth } from '../alerts/columns';
import type { ColumnDefinition } from '../alerts/columns';
import { formatAge } from '../alerts/format';
import { orderedGroupEntries } from '../alerts/grouping';
import { SEVERITY_LABELS } from '../alerts/severity';
import type { AlertSummary } from '../alerts/types';
import type { GroupChildren } from '../hooks/useGroupChildren';
import { groupId } from '../hooks/useGroupChildren';
import { ColumnResizeHandle } from './ColumnResizeHandle';
import { EmptyState } from './EmptyState';
import { ChevronIcon, SeverityIcon } from './icons';
import { ColumnHeader } from './AlertTable';
import type { AlertPagination } from './AlertTable';

/**
 * The grouped view of the alert table.
 *
 * A group row summarises every alert sharing a key combination; expanding it
 * loads the members and renders them with the operator's own columns. A group
 * of one has nothing to collapse into, so it renders with no chevron and its
 * activation opens the single member's detail directly — and since its
 * aggregates are just that member's own values, the row shows those values in
 * the operator's columns whenever the member is already loaded.
 *
 * Marked up as a treegrid rather than a plain table: the hierarchy is the
 * point, and without aria-expanded and aria-level a screen reader hears a flat
 * list of rows whose indentation means nothing.
 *
 * Column headers after the group heading are the flat table's own: sortable
 * columns offer the same APG sort button (the sort rides the same alerts
 * query, so it orders expanded members and follows the console back to the
 * flat view), and every column keeps its resize handle.
 */

export interface AlertGroupTableProps {
  groups: readonly AlertGroupSummary[];
  children: Record<string, GroupChildren>;
  /**
   * The operator's grouping keys, in order. Headings name their key values in
   * this order; the payload's own key order is the server's (alphabetical),
   * not the one it grouped by.
   */
  groupKeys?: readonly string[];
  columns?: readonly ColumnDefinition[];
  /** Resized widths keyed by column id; shared with the flat table. */
  columnWidths?: Readonly<Record<string, number>>;
  onColumnResize?: (columnId: string, width: number) => void;
  onColumnResizeReset?: (columnId: string) => void;
  filterActive?: boolean;
  filterQuery?: string;
  onClearFilter?: () => void;
  pagination?: AlertPagination;
  selectedId?: string | null;
  /** Active server-side sort; the matching header exposes it via aria-sort. */
  sort?: AlertSort | null;
  /** Header activation requests a server-side sort for that column. */
  onSortChange?: (sort: AlertSort) => void;
  /**
   * Resolves an alert id to its already-loaded row. A one-member group uses
   * it to fill its columns from the member itself; when the member is not
   * loaded (or no resolver is given) the row keeps the aggregate summary.
   */
  memberFor?: (alertId: string) => AlertSummary | undefined;
  onExpand: (key: Record<string, string>) => void;
  onCollapse: (key: Record<string, string>) => void;
  onLoadMoreChildren: (key: Record<string, string>) => void;
  onSelect?: (alert: AlertSummary) => void;
  /** Opens an alert by id; a one-member group activates straight into it. */
  onOpenAlert?: (alertId: string) => void;
}

/**
 * A group's heading: `name=value` for each grouping key, in the order the
 * console asked for the grouping. Naming the keys keeps a custom grouping
 * readable — values alone are ambiguous once the operator picks the keys.
 */
export function groupLabel(key: Record<string, string>, order: readonly string[] = []): string {
  return orderedGroupEntries(key, order)
    .map(([name, value]) => `${name}=${value === '' ? '—' : value}`)
    .join(' · ');
}

export function AlertGroupTable({
  groups,
  children,
  groupKeys = [],
  columns = FIXED_COLUMNS,
  columnWidths = {},
  onColumnResize,
  onColumnResizeReset,
  filterActive = false,
  filterQuery = '',
  onClearFilter,
  pagination,
  selectedId = null,
  sort = null,
  onSortChange,
  memberFor,
  onExpand,
  onCollapse,
  onLoadMoreChildren,
  onSelect,
  onOpenAlert,
}: AlertGroupTableProps) {
  // One heading cell replaces the first child column, so the grid is as wide as
  // the operator's column set.
  const gridWidth = Math.max(columns.length, 2);
  const firstColumn = columns.at(0);
  const resizable = onColumnResize !== undefined && onColumnResizeReset !== undefined;

  return (
    <div className="table-panel">
      <div className="table-scroll">
        <table className="alert-table alert-group-table" role="treegrid">
          <caption>Alert groups ({groups.length})</caption>
          <colgroup>
            {columns.map((column, index) => {
              // The first column renders the group heading — chevron, severity
              // tag, key, count — which is far wider than the severity tag its
              // basis is sized for, so by default it flexes with the long-text
              // columns. A stored width still wins: the heading's resize handle
              // is the first column's.
              const width =
                index === 0
                  ? columnWidths[column.id]
                  : columnWidth(column, columnWidths[column.id]);
              return (
                <col
                  key={column.id}
                  className={column.optional ? 'col-optional' : undefined}
                  style={width !== undefined ? { width: `${width}px` } : undefined}
                />
              );
            })}
          </colgroup>
          <thead>
            <tr>
              {/* The heading column stands in for the first child column, so it
                  also carries that column's stored width and resize handle. It
                  stays unsortable: it renders the group key, not an alert
                  field the server can order by. */}
              <th scope="col">
                Alert group
                {resizable && firstColumn !== undefined ? (
                  <ColumnResizeHandle
                    columnId={firstColumn.id}
                    columnLabel="Alert group"
                    width={columnWidths[firstColumn.id]}
                    onResize={onColumnResize}
                    onReset={onColumnResizeReset}
                  />
                ) : null}
              </th>
              {columns.slice(1).map((column) => (
                <ColumnHeader
                  key={column.id}
                  column={column}
                  sort={sort}
                  onSortChange={onSortChange}
                  width={columnWidths[column.id]}
                  onResize={resizable ? onColumnResize : undefined}
                  onResizeReset={resizable ? onColumnResizeReset : undefined}
                />
              ))}
            </tr>
          </thead>
          <tbody>
            {groups.length === 0 ? (
              <tr className="empty-row">
                <td colSpan={gridWidth}>
                  <EmptyState
                    filterActive={filterActive}
                    query={filterQuery}
                    onClearFilter={onClearFilter}
                  />
                </td>
              </tr>
            ) : (
              groups.map((group) => (
                <GroupRows
                  key={groupId(group.key)}
                  group={group}
                  groupKeys={groupKeys}
                  columns={columns}
                  loaded={children[groupId(group.key)]}
                  selectedId={selectedId}
                  memberFor={memberFor}
                  onExpand={onExpand}
                  onCollapse={onCollapse}
                  onLoadMoreChildren={onLoadMoreChildren}
                  onSelect={onSelect}
                  onOpenAlert={onOpenAlert}
                />
              ))
            )}
          </tbody>
        </table>
      </div>
      {pagination !== undefined ? <GroupTableFoot pagination={pagination} /> : null}
    </div>
  );
}

function GroupRows({
  group,
  groupKeys,
  columns,
  loaded,
  selectedId,
  memberFor,
  onExpand,
  onCollapse,
  onLoadMoreChildren,
  onSelect,
  onOpenAlert,
}: {
  group: AlertGroupSummary;
  groupKeys: readonly string[];
  columns: readonly ColumnDefinition[];
  loaded: GroupChildren | undefined;
  selectedId: string | null;
  memberFor?: (alertId: string) => AlertSummary | undefined;
  onExpand: (key: Record<string, string>) => void;
  onCollapse: (key: Record<string, string>) => void;
  onLoadMoreChildren: (key: Record<string, string>) => void;
  onSelect?: (alert: AlertSummary) => void;
  onOpenAlert?: (alertId: string) => void;
}) {
  const expandable = group.total > 1;
  // A one-member group has nothing to expand into; activating the row opens
  // that member's detail instead of going through a one-row expansion.
  const openable = !expandable && onOpenAlert !== undefined;
  const expanded = loaded !== undefined;
  const label = groupLabel(group.key, groupKeys);
  // A one-member group's aggregates are that member's own values, so when the
  // member is already loaded the row renders them in the operator's columns
  // (summary, instance, …) instead of a severity mix that says nothing new.
  const member = expandable ? undefined : memberFor?.(group.sampleAlertId);

  const activate = () => {
    if (expandable) {
      if (expanded) {
        onCollapse(group.key);
      } else {
        onExpand(group.key);
      }
    } else {
      onOpenAlert?.(group.sampleAlertId);
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTableRowElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      activate();
    }
  };

  return (
    <>
      <tr
        className={`group-row${expanded ? ' group-row-expanded' : ''}`}
        aria-expanded={expandable ? expanded : undefined}
        aria-level={1}
        tabIndex={expandable || openable ? 0 : undefined}
        data-group={groupId(group.key)}
        onClick={activate}
        onKeyDown={handleKeyDown}
      >
        <th scope="row" className="group-cell">
          <span className="group-heading">
            {expandable ? (
              <ChevronIcon className={`group-chevron${expanded ? ' is-open' : ''}`} />
            ) : (
              <span className="group-chevron-spacer" aria-hidden="true" />
            )}
            <span className={`sev-tag sev-${group.worstSeverity}`}>
              <SeverityIcon severity={group.worstSeverity} />
              <span>{group.worstSeverityLabel ?? SEVERITY_LABELS[group.worstSeverity]}</span>
            </span>
            <span className="group-name">{label}</span>
            {expandable ? <span className="group-count">{group.total}</span> : null}
          </span>
        </th>
        {member !== undefined ? (
          // One member: the row is that alert, so it renders exactly like a
          // child row and lines up with the columns one for one.
          columns.slice(1).map((column) => {
            const cell = column.cell(member);
            const className = [cell.className ?? '', column.optional ? 'col-optional' : '']
              .filter((part) => part !== '')
              .join(' ');
            return (
              <td key={column.id} className={className === '' ? undefined : className}>
                {cell.text}
              </td>
            );
          })
        ) : (
          <>
            {/* The header is one heading cell plus the child columns after the
                first, so a group row is heading + summary + age + ack: the
                summary takes whatever is left, or the row would not line up
                with it. */}
            <td className="cell-mono group-summary" colSpan={Math.max(columns.length - 3, 1)}>
              <GroupSeverityMix counts={group.severityCounts} />
            </td>
            <td className="cell-mono">{formatAge(group.earliestStartsAt)}</td>
            <td className="cell-mono group-ack">
              {group.acknowledged > 0 ? `${group.acknowledged}/${group.total}` : '—'}
            </td>
          </>
        )}
      </tr>
      {expanded ? (
        <ChildRows
          group={group}
          columns={columns}
          loaded={loaded}
          selectedId={selectedId}
          onLoadMoreChildren={onLoadMoreChildren}
          onSelect={onSelect}
        />
      ) : null}
    </>
  );
}

function ChildRows({
  group,
  columns,
  loaded,
  selectedId,
  onLoadMoreChildren,
  onSelect,
}: {
  group: AlertGroupSummary;
  columns: readonly ColumnDefinition[];
  loaded: GroupChildren;
  selectedId: string | null;
  onLoadMoreChildren: (key: Record<string, string>) => void;
  onSelect?: (alert: AlertSummary) => void;
}) {
  const remaining = loaded.total - loaded.alerts.length;
  return (
    <>
      {loaded.alerts.map((alert) => (
        <tr
          key={alert.id}
          className={`child-row${alert.id === selectedId ? ' row-selected' : ''}`}
          aria-level={2}
          tabIndex={onSelect === undefined ? undefined : 0}
          data-alert-id={alert.id}
          onClick={() => onSelect?.(alert)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              onSelect?.(alert);
            }
          }}
        >
          {columns.map((column, index) => {
            const cell = column.cell(alert);
            const className = [
              index === 0 ? 'child-indent' : '',
              cell.className ?? '',
              column.optional ? 'col-optional' : '',
            ]
              .filter((part) => part !== '')
              .join(' ');
            return (
              <td key={column.id} className={className === '' ? undefined : className}>
                {cell.text}
              </td>
            );
          })}
        </tr>
      ))}
      {loaded.loading ? (
        <tr className="child-row child-status" aria-level={2}>
          <td colSpan={columns.length} className="child-indent">
            Loading members…
          </td>
        </tr>
      ) : null}
      {loaded.error !== null ? (
        <tr className="child-row child-status" aria-level={2}>
          <td colSpan={columns.length} className="child-indent">
            <span role="alert">Could not load members: {loaded.error.message}</span>
            <button type="button" className="button" onClick={() => onLoadMoreChildren(group.key)}>
              Retry
            </button>
          </td>
        </tr>
      ) : null}
      {!loaded.loading && loaded.nextCursor !== '' ? (
        <tr className="child-row child-status" aria-level={2}>
          <td colSpan={columns.length} className="child-indent">
            <button type="button" className="button" onClick={() => onLoadMoreChildren(group.key)}>
              Load {remaining > 0 ? `${remaining} more` : 'more'}
            </button>
          </td>
        </tr>
      ) : null}
    </>
  );
}

function GroupSeverityMix({ counts }: { counts: Record<string, number> }) {
  const entries = Object.entries(counts).filter(([, count]) => count > 0);
  if (entries.length === 0) {
    return <>—</>;
  }
  return (
    <span className="group-mix">
      {entries.map(([severity, count]) => (
        <span key={severity} className={`group-mix-part sev-${severity}`}>
          {count} {severity}
        </span>
      ))}
    </span>
  );
}

function GroupTableFoot({ pagination }: { pagination: AlertPagination }) {
  const { loaded, total, hasMore, loadingMore, error, onLoadMore } = pagination;
  return (
    <div className="table-foot">
      <p className="table-foot-count">
        Showing {loaded} of {total} groups
      </p>
      {error !== null ? (
        <p className="table-foot-error" role="alert">
          Could not load more groups: {error.message}
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
