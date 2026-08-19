import { describe, expect, it } from 'vitest';
import {
  DEFAULT_COLUMN_IDS,
  FIXED_COLUMNS,
  columnWidth,
  labelColumn,
  resolveColumns,
} from './columns';
import type { AlertSummary } from './types';

const alert: AlertSummary = {
  id: 'a1',
  severity: 'critical',
  state: 'firing',
  name: 'Cardinality',
  summary: 'too many series',
  team: 'platform',
  instance: 'api-1',
  source: 'yul',
  startsAt: new Date().toISOString(),
  notes: 0,
  labels: { alertname: 'Cardinality', prometheus_cluster: 'yul', name: 'windows_service_status' },
  suppressed: false,
  lastSeen: new Date().toISOString(),
};

describe('column registry', () => {
  it('resolves stored ids in the order they were saved', () => {
    const resolved = resolveColumns(['age', 'severity', 'alert']);
    expect(resolved.map((column) => column.id)).toEqual(['age', 'severity', 'alert']);
  });

  it('renders a column bound to any label', () => {
    const column = labelColumn('label:name');
    expect(column?.label).toBe('name');
    expect(column?.cell(alert).text).toBe('windows_service_status');
  });

  it('shows a placeholder when the alert lacks the label', () => {
    const column = labelColumn('label:absent');
    expect(column?.cell(alert).text).toBe('—');
  });

  it('drops ids it cannot render rather than failing the table', () => {
    // A layout saved by a newer console, or naming a column since removed,
    // should cost that column and not the whole table.
    const resolved = resolveColumns(['severity', 'invented', 'label:', 'age']);
    expect(resolved.map((column) => column.id)).toEqual(['severity', 'age']);
  });

  it('falls back to the built-in set when nothing resolves', () => {
    expect(resolveColumns(['invented']).map((column) => column.id)).toEqual([
      ...DEFAULT_COLUMN_IDS,
    ]);
  });

  it('ignores a repeated id', () => {
    expect(resolveColumns(['age', 'age']).map((column) => column.id)).toEqual(['age']);
  });

  it('marks only server-sortable columns with a sort field', () => {
    const assignee = FIXED_COLUMNS.find((column) => column.id === 'assignee');
    expect(assignee?.sortField).toBeUndefined();
    // Sorting by an arbitrary label would be a sequential scan, so a label
    // column carries no sort until an index exists for it.
    expect(labelColumn('label:name')?.sortField).toBeUndefined();
  });
});

describe('column sizing', () => {
  it('bases the short columns and leaves the long-text ones flexible', () => {
    const based = ['severity', 'state', 'team', 'lastSeen', 'source', 'age', 'assignee', 'notes'];
    const flexible = ['alert', 'summary', 'instance'];
    for (const column of FIXED_COLUMNS) {
      if (based.includes(column.id)) {
        expect(column.basis, column.id).toBeGreaterThanOrEqual(64);
      } else {
        expect(flexible, column.id).toContain(column.id);
        expect(column.basis, column.id).toBeUndefined();
      }
    }
  });

  it('keeps label columns flexible: their content length is unknowable', () => {
    expect(labelColumn('label:name')?.basis).toBeUndefined();
  });

  it('starts at the basis, defers to a stored width, and flexes without either', () => {
    const severity = FIXED_COLUMNS.find((column) => column.id === 'severity');
    const summary = FIXED_COLUMNS.find((column) => column.id === 'summary');
    expect(columnWidth(severity!, undefined)).toBe(severity!.basis);
    expect(columnWidth(severity!, 240)).toBe(240);
    expect(columnWidth(summary!, undefined)).toBeUndefined();
  });
});

describe('lifecycle columns', () => {
  it('shows how long the source has been quiet', () => {
    const column = FIXED_COLUMNS.find((entry) => entry.id === 'lastSeen');
    expect(column?.label).toBe('Last seen');
    // An operator questioning an expired alert should see the evidence.
    expect(
      column?.cell({ ...alert, lastSeen: new Date(Date.now() - 3 * 3600_000).toISOString() }).text,
    ).toMatch(/h$/);
    expect(column?.cell({ ...alert, lastSeen: '' }).text).toBe('—');
  });
});
