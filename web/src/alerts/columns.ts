import { formatAge } from './format';
import { SEVERITY_LABELS } from './severity';
import type { AlertSort, AlertSortField } from './api';
import type { AlertSummary } from './types';

/**
 * The console's column registry. Columns used to be a constant the table
 * rendered positionally; making them data is what allows an operator to choose
 * which ones they keep and to add a column bound to any alert label, without
 * that label needing console-wide meaning.
 *
 * A cell is described here rather than in the table so a column is one entry in
 * one place: its heading, whether the server can sort it, and how it renders.
 */

/** How a cell renders: the text, plus the class the table puts on the `td`. */
export interface ColumnCell {
  text: string;
  className?: string;
}

export interface ColumnDefinition {
  id: string;
  label: string;
  /** Optional columns collapse first on narrow viewports. */
  optional: boolean;
  /** Set when the alerts endpoint can sort by this column. */
  sortField?: AlertSortField;
  cell: (alert: AlertSummary) => ColumnCell;
}

/** Marks a column bound to an arbitrary alert label. Matches the server. */
export const LABEL_COLUMN_PREFIX = 'label:';

const EMPTY = '—';

function present(value: string | undefined): string {
  return value === undefined || value === '' ? EMPTY : value;
}

/** The built-in columns, in the order a console shows them by default. */
export const FIXED_COLUMNS: readonly ColumnDefinition[] = [
  {
    id: 'severity',
    label: 'Severity',
    optional: false,
    sortField: 'severity',
    cell: (alert) => ({ text: alert.severityLabel ?? SEVERITY_LABELS[alert.severity] }),
  },
  {
    id: 'state',
    label: 'State',
    optional: false,
    sortField: 'state',
    cell: (alert) => ({ text: alert.state }),
  },
  {
    id: 'alert',
    label: 'Alert',
    optional: false,
    sortField: 'name',
    cell: (alert) => ({ text: alert.name, className: 'cell-name' }),
  },
  {
    id: 'summary',
    label: 'Summary',
    optional: true,
    sortField: 'summary',
    cell: (alert) => ({ text: alert.summary, className: 'cell-summary' }),
  },
  {
    id: 'team',
    label: 'Team',
    optional: true,
    sortField: 'team',
    cell: (alert) => ({ text: present(alert.team), className: 'cell-mono' }),
  },
  {
    id: 'instance',
    label: 'Instance',
    optional: true,
    sortField: 'instance',
    cell: (alert) => ({ text: present(alert.instance), className: 'cell-mono' }),
  },
  {
    id: 'source',
    label: 'Source',
    optional: false,
    sortField: 'source',
    cell: (alert) => ({ text: alert.source, className: 'cell-mono' }),
  },
  {
    id: 'age',
    label: 'Age',
    optional: false,
    sortField: 'age',
    cell: (alert) => ({ text: formatAge(alert.startsAt), className: 'cell-mono' }),
  },
  {
    id: 'assignee',
    label: 'Assignee',
    optional: true,
    cell: (alert) => ({ text: present(alert.assignee), className: 'cell-mono' }),
  },
  {
    id: 'notes',
    label: 'Notes',
    optional: true,
    cell: (alert) => ({
      text: alert.notes > 0 ? String(alert.notes) : EMPTY,
      className: 'cell-mono',
    }),
  },
];

export const DEFAULT_COLUMN_IDS: readonly string[] = FIXED_COLUMNS.map((column) => column.id);

/**
 * A column bound to an alert label. It carries no server-side sort: sorting by
 * an arbitrary label would be a sequential scan, so the header stays plain
 * until an index exists for it.
 */
export function labelColumn(id: string): ColumnDefinition | null {
  const name = id.slice(LABEL_COLUMN_PREFIX.length);
  if (name === '') {
    return null;
  }
  return {
    id,
    label: name,
    optional: true,
    cell: (alert) => ({ text: present(alert.labels[name]), className: 'cell-mono' }),
  };
}

/**
 * Resolves stored column ids into definitions. Unknown ids are dropped rather
 * than throwing: a layout saved by a newer console, or naming a column since
 * removed, should cost that one column and not the whole table.
 */
export function resolveColumns(ids: readonly string[]): ColumnDefinition[] {
  const fixed = new Map(FIXED_COLUMNS.map((column) => [column.id, column]));
  const resolved: ColumnDefinition[] = [];
  const seen = new Set<string>();
  for (const id of ids) {
    if (seen.has(id)) {
      continue;
    }
    seen.add(id);
    if (id.startsWith(LABEL_COLUMN_PREFIX)) {
      const column = labelColumn(id);
      if (column !== null) {
        resolved.push(column);
      }
      continue;
    }
    const column = fixed.get(id);
    if (column !== undefined) {
      resolved.push(column);
    }
  }
  return resolved.length > 0 ? resolved : [...FIXED_COLUMNS];
}

/** Next sort when a header is activated: inactive starts ascending, active toggles. */
export function nextSort(field: AlertSortField, current: AlertSort | null): AlertSort {
  if (current !== null && current.field === field) {
    return { field, order: current.order === 'asc' ? 'desc' : 'asc' };
  }
  return { field, order: 'asc' };
}
