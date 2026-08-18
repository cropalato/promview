import type { KeyboardEvent } from 'react';
import type { AlertGroupSummary, AlertSort } from '../alerts/api';
import { FIXED_COLUMNS } from '../alerts/columns';
import type { ColumnDefinition } from '../alerts/columns';
import { formatAge } from '../alerts/format';
import { SEVERITY_LABELS } from '../alerts/severity';
import type { AlertSummary } from '../alerts/types';
import type { GroupChildren } from '../hooks/useGroupChildren';
import { groupId } from '../hooks/useGroupChildren';
import { EmptyState } from './EmptyState';
import { ChevronIcon, SeverityIcon } from './icons';
import type { AlertPagination } from './AlertTable';

/**
 * The grouped view of the alert table.
 *
 * A group row summarises every alert sharing a key combination; expanding it
 * loads the members and renders them with the operator's own columns. A group
 * of one renders as an ordinary row with no chevron, so grouping adds no
 * ceremony where there is nothing to collapse.
 *
 * Marked up as a treegrid rather than a plain table: the hierarchy is the
 * point, and without aria-expanded and aria-level a screen reader hears a flat
 * list of rows whose indentation means nothing.
 */

export interface AlertGroupTableProps {
  groups: readonly AlertGroupSummary[];
  children: Record<string, GroupChildren>;
  columns?: readonly ColumnDefinition[];
  filterActive?: boolean;
  filterQuery?: string;
  onClearFilter?: () => void;
  pagination?: AlertPagination;
  selectedId?: string | null;
  sort?: AlertSort | null;
  onExpand: (key: Record<string, string>) => void;
  onCollapse: (key: Record<string, string>) => void;
  onLoadMoreChildren: (key: Record<string, string>) => void;
  onSelect?: (alert: AlertSummary) => void;
}

/** A group's heading: its key values, in the order the server grouped them. */
export function groupLabel(key: Record<string, string>): string {
  return Object.values(key)
    .map((value) => (value === '' ? '—' : value))
    .join(' · ');
}

export function AlertGroupTable({
  groups,
  children,
  columns = FIXED_COLUMNS,
  filterActive = false,
  filterQuery = '',
  onClearFilter,
  pagination,
  selectedId = null,
  onExpand,
  onCollapse,
  onLoadMoreChildren,
  onSelect,
}: AlertGroupTableProps) {
  // One heading cell replaces the first child column, so the grid is as wide as
  // the operator's column set.
  const gridWidth = Math.max(columns.length, 2);

  return (
    <div className="table-panel">
      <div className="table-scroll">
        <table className="alert-table alert-group-table" role="treegrid">
          <caption>Alert groups ({groups.length})</caption>
          <thead>
            <tr>
              <th scope="col">Alert group</th>
              {columns.slice(1).map((column) => (
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
                  columns={columns}
                  loaded={children[groupId(group.key)]}
                  selectedId={selectedId}
                  onExpand={onExpand}
                  onCollapse={onCollapse}
                  onLoadMoreChildren={onLoadMoreChildren}
                  onSelect={onSelect}
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
  columns,
  loaded,
  selectedId,
  onExpand,
  onCollapse,
  onLoadMoreChildren,
  onSelect,
}: {
  group: AlertGroupSummary;
  columns: readonly ColumnDefinition[];
  loaded: GroupChildren | undefined;
  selectedId: string | null;
  onExpand: (key: Record<string, string>) => void;
  onCollapse: (key: Record<string, string>) => void;
  onLoadMoreChildren: (key: Record<string, string>) => void;
  onSelect?: (alert: AlertSummary) => void;
}) {
  const expandable = group.total > 1;
  const expanded = loaded !== undefined;
  const label = groupLabel(group.key);

  const toggle = () => {
    if (!expandable) {
      return;
    }
    if (expanded) {
      onCollapse(group.key);
    } else {
      onExpand(group.key);
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTableRowElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggle();
    }
  };

  return (
    <>
      <tr
        className={`group-row${expanded ? ' group-row-expanded' : ''}`}
        aria-expanded={expandable ? expanded : undefined}
        aria-level={1}
        tabIndex={expandable ? 0 : undefined}
        data-group={groupId(group.key)}
        onClick={toggle}
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
        {/* The header is one heading cell plus the child columns after the
            first, so a group row is heading + summary + age + ack: the summary
            takes whatever is left, or the row would not line up with it. */}
        <td className="cell-mono group-summary" colSpan={Math.max(columns.length - 3, 1)}>
          <GroupSeverityMix counts={group.severityCounts} />
        </td>
        <td className="cell-mono">{formatAge(group.earliestStartsAt)}</td>
        <td className="cell-mono group-ack">
          {group.acknowledged > 0 ? `${group.acknowledged}/${group.total}` : '—'}
        </td>
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
