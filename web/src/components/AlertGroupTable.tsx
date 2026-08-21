import type { KeyboardEvent } from 'react';
import type { AlertGroupSummary, AlertSort } from '../alerts/api';
import { FIXED_COLUMNS, LABEL_COLUMN_PREFIX, columnWidth } from '../alerts/columns';
import type { ColumnDefinition } from '../alerts/columns';
import { formatAge } from '../alerts/format';
import { SEVERITY_LABELS } from '../alerts/severity';
import type { AlertSummary } from '../alerts/types';
import type { GroupChildren } from '../hooks/useGroupChildren';
import { groupId } from '../hooks/useGroupChildren';
import { EmptyState } from './EmptyState';
import { ChevronIcon, SeverityIcon } from './icons';
import { ColumnHeader } from './AlertTable';
import type { AlertPagination } from './AlertTable';

/**
 * The grouped view of the alert table.
 *
 * A group row summarises every alert sharing a key combination; expanding it
 * loads the members and renders them with the operator's own columns. One
 * fixed group-control column holds the tree affordance and aggregates; every
 * selected operator column follows it in its saved order. A group of one has
 * nothing to collapse into, so it renders with no chevron and its activation
 * opens the single member's detail directly — and since its aggregates are
 * just that member's own values, the row shows those values in the operator's
 * columns whenever the member is already loaded.
 *
 * Marked up as a treegrid rather than a plain table: the hierarchy is the
 * point, and without aria-expanded and aria-level a screen reader hears a flat
 * list of rows whose indentation means nothing.
 *
 * The operator's column headers are the flat table's own: sortable columns
 * offer the same APG sort button (the sort rides the same alerts query, so it
 * orders expanded members and follows the console back to the flat view), and
 * every operator column keeps its resize handle.
 */

export interface AlertGroupTableProps {
  groups: readonly AlertGroupSummary[];
  children: Record<string, GroupChildren>;
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
   * loaded (or no resolver is given) the control cell keeps group aggregates.
   */
  memberFor?: (alertId: string) => AlertSummary | undefined;
  onExpand: (key: Record<string, string>) => void;
  onCollapse: (key: Record<string, string>) => void;
  onLoadMoreChildren: (key: Record<string, string>) => void;
  onSelect?: (alert: AlertSummary) => void;
  /** Opens an alert by id; a one-member group activates straight into it. */
  onOpenAlert?: (alertId: string) => void;
  /**
   * Opens the silence dialog for a whole group. Absent when the operator may
   * not silence, or the deployment has no Alertmanager to write to, and the
   * per-row control is then not rendered at all.
   */
  onSilenceGroup?: (group: AlertGroupSummary) => void;
}

const GROUP_CONTROL_COLUMNS = 1;

/** The normal alert column that displays each built-in grouping key. */
const GROUP_KEY_COLUMNS: Readonly<Record<string, string>> = {
  alert: 'alertname',
  source: 'source',
  team: 'team',
  instance: 'instance',
  severity: 'severity',
};

export function AlertGroupTable({
  groups,
  children,
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
  onSilenceGroup,
}: AlertGroupTableProps) {
  // The group-control cell precedes every operator column, preserving their
  // saved order and their one-to-one position across every grouped row type.
  const gridWidth = columns.length + GROUP_CONTROL_COLUMNS;
  const resizable = onColumnResize !== undefined && onColumnResizeReset !== undefined;

  return (
    <div className="table-panel">
      <div className="table-scroll">
        <table className="alert-table alert-group-table" role="treegrid">
          <caption>Alert groups ({groups.length})</caption>
          <colgroup>
            <col className="group-control-column" />
            {columns.map((column) => {
              const width = columnWidth(column, columnWidths[column.id]);
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
              <th scope="col">Alert group</th>
              {columns.map((column) => (
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
                  columns={columns}
                  loaded={children[groupId(group.key)]}
                  selectedId={selectedId}
                  memberFor={memberFor}
                  onExpand={onExpand}
                  onCollapse={onCollapse}
                  onLoadMoreChildren={onLoadMoreChildren}
                  onSelect={onSelect}
                  onOpenAlert={onOpenAlert}
                  onSilenceGroup={onSilenceGroup}
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
  memberFor,
  onExpand,
  onCollapse,
  onLoadMoreChildren,
  onSelect,
  onOpenAlert,
  onSilenceGroup,
}: {
  group: AlertGroupSummary;
  columns: readonly ColumnDefinition[];
  loaded: GroupChildren | undefined;
  selectedId: string | null;
  memberFor?: (alertId: string) => AlertSummary | undefined;
  onExpand: (key: Record<string, string>) => void;
  onCollapse: (key: Record<string, string>) => void;
  onLoadMoreChildren: (key: Record<string, string>) => void;
  onSelect?: (alert: AlertSummary) => void;
  onOpenAlert?: (alertId: string) => void;
  onSilenceGroup?: (group: AlertGroupSummary) => void;
}) {
  const expandable = group.total > 1;
  // A one-member group has nothing to expand into; activating the row opens
  // that member's detail instead of going through a one-row expansion.
  const openable = !expandable && onOpenAlert !== undefined;
  const expanded = loaded !== undefined;
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
            {expandable ? <span className="group-count">{group.total}</span> : null}
            {onSilenceGroup !== undefined ? (
              // Stops the row's own activation: silencing a group and expanding
              // it are different intentions, and the click lands on both.
              <button
                type="button"
                className="button group-silence"
                aria-label={`Silence ${describeGroupKey(group.key)}`}
                title="Silence this group"
                onClick={(event) => {
                  event.stopPropagation();
                  onSilenceGroup(group);
                }}
              >
                Silence
              </button>
            ) : null}
          </span>
          {member === undefined ? (
            <span className="group-aggregate-values cell-mono">
              <GroupSeverityMix counts={group.severityCounts} />
              <span>{formatAge(group.earliestStartsAt)}</span>
              <span
                className="group-ack"
                aria-label={
                  group.acknowledged > 0
                    ? `${group.acknowledged} of ${group.total} acknowledged`
                    : 'No acknowledged members'
                }
              >
                {group.acknowledged > 0 ? `${group.acknowledged}/${group.total}` : '—'}
              </span>
            </span>
          ) : null}
        </th>
        {member !== undefined ? (
          // One member: the row is that alert, so it renders exactly like a
          // child row and lines up with the columns one for one.
          <AlertCells alert={member} columns={columns} />
        ) : (
          <GroupKeyCells group={group} columns={columns} />
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
          <td className="child-group-cell" />
          <AlertCells alert={alert} columns={columns} />
        </tr>
      ))}
      {loaded.loading ? (
        <tr className="child-row child-status" aria-level={2}>
          <td colSpan={columns.length + GROUP_CONTROL_COLUMNS} className="child-indent">
            Loading members…
          </td>
        </tr>
      ) : null}
      {loaded.error !== null ? (
        <tr className="child-row child-status" aria-level={2}>
          <td colSpan={columns.length + GROUP_CONTROL_COLUMNS} className="child-indent">
            <span role="alert">Could not load members: {loaded.error.message}</span>
            <button type="button" className="button" onClick={() => onLoadMoreChildren(group.key)}>
              Retry
            </button>
          </td>
        </tr>
      ) : null}
      {!loaded.loading && loaded.nextCursor !== '' ? (
        <tr className="child-row child-status" aria-level={2}>
          <td colSpan={columns.length + GROUP_CONTROL_COLUMNS} className="child-indent">
            <button type="button" className="button" onClick={() => onLoadMoreChildren(group.key)}>
              Load {remaining > 0 ? `${remaining} more` : 'more'}
            </button>
          </td>
        </tr>
      ) : null}
    </>
  );
}

/** Cells shared by loaded singleton groups and expanded member rows. */
function AlertCells({
  alert,
  columns,
}: {
  alert: AlertSummary;
  columns: readonly ColumnDefinition[];
}) {
  return columns.map((column) => {
    const cell = column.cell(alert);
    const className = [cell.className ?? '', column.optional ? 'col-optional' : '']
      .filter((part) => part !== '')
      .join(' ');
    return (
      <td key={column.id} className={className === '' ? undefined : className}>
        {cell.text}
      </td>
    );
  });
}

/**
 * Group keys stay in their own normal columns. Besides the built-in mappings,
 * a `label:<key>` column follows the label-column registry and shows that same
 * key when the group was formed from it.
 */
function GroupKeyCells({
  group,
  columns,
}: {
  group: AlertGroupSummary;
  columns: readonly ColumnDefinition[];
}) {
  return columns.map((column) => {
    const key = groupKeyForColumn(column.id);
    const value =
      key === undefined || !(key in group.key) ? undefined : presentGroupValue(group.key[key]);
    const className = [
      value !== undefined ? (column.id === 'alert' ? 'cell-name' : 'cell-mono') : '',
      column.optional ? 'col-optional' : '',
    ]
      .filter((part) => part !== '')
      .join(' ');
    return (
      <td
        key={column.id}
        className={className === '' ? undefined : className}
        data-group-key={value === undefined ? undefined : key}
      >
        {value}
      </td>
    );
  });
}

/** Names a group in the words its key uses, for the silence control's label. */
function describeGroupKey(key: Record<string, string>): string {
  return Object.entries(key)
    .map(([name, value]) => `${name}=${value}`)
    .join(', ');
}

function groupKeyForColumn(columnId: string): string | undefined {
  const fixed = GROUP_KEY_COLUMNS[columnId];
  if (fixed !== undefined) {
    return fixed;
  }
  if (!columnId.startsWith(LABEL_COLUMN_PREFIX)) {
    return undefined;
  }
  const label = columnId.slice(LABEL_COLUMN_PREFIX.length);
  return label === '' ? undefined : label;
}

function presentGroupValue(value: string | undefined): string {
  return value === undefined || value === '' ? '—' : value;
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
