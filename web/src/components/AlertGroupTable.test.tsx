import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { AlertGroupSummary } from '../alerts/api';
import type { AlertSummary } from '../alerts/types';
import type { GroupChildren } from '../hooks/useGroupChildren';
import { AlertGroupTable } from './AlertGroupTable';

function group(overrides: Partial<AlertGroupSummary> = {}): AlertGroupSummary {
  return {
    key: { alertname: 'Cardinality', source: 'yul' },
    total: 52,
    acknowledged: 3,
    severityCounts: { critical: 1, warning: 51 },
    worstSeverity: 'critical',
    latestLastSeen: new Date().toISOString(),
    earliestStartsAt: new Date(Date.now() - 60 * 60_000).toISOString(),
    sampleAlertId: '42',
    ...overrides,
  };
}

function member(id: string, instance: string): AlertSummary {
  return {
    id,
    severity: 'warning',
    state: 'firing',
    name: 'Cardinality',
    summary: 'too many series',
    team: 'platform',
    instance,
    source: 'yul',
    startsAt: new Date().toISOString(),
    notes: 0,
    labels: { alertname: 'Cardinality', instance },
    suppressed: false,
    lastSeen: new Date().toISOString(),
  };
}

function children(overrides: Partial<GroupChildren> = {}): Record<string, GroupChildren> {
  return {
    [JSON.stringify({ alertname: 'Cardinality', source: 'yul' })]: {
      alerts: [member('1', 'a'), member('2', 'b')],
      nextCursor: '',
      total: 2,
      loading: false,
      error: null,
      ...overrides,
    },
  };
}

const noop = () => {};

describe('AlertGroupTable', () => {
  it('summarises a group without loading its members', () => {
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
      />,
    );

    const row = screen.getByRole('row', { name: /Cardinality/ });
    expect(row).toHaveAttribute('aria-expanded', 'false');
    expect(within(row).getByText('52')).toBeInTheDocument();
    expect(within(row).getByText('51 warning')).toBeInTheDocument();
    // Acknowledgement coverage is what an operator scans a collapsed row for.
    expect(within(row).getByText('3/52')).toBeInTheDocument();
  });

  it('expands and collapses on activation', () => {
    const onExpand = vi.fn();
    const onCollapse = vi.fn();
    const { rerender } = render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={onExpand}
        onCollapse={onCollapse}
        onLoadMoreChildren={noop}
      />,
    );

    fireEvent.click(screen.getByRole('row', { name: /Cardinality/ }));
    expect(onExpand).toHaveBeenCalledWith({ alertname: 'Cardinality', source: 'yul' });

    rerender(
      <AlertGroupTable
        groups={[group()]}
        children={children()}
        onExpand={onExpand}
        onCollapse={onCollapse}
        onLoadMoreChildren={noop}
      />,
    );
    // Member rows also mention the alert name; the group row is the one that
    // carries the whole key.
    const row = screen.getByRole('row', { name: /Cardinality · yul/ });
    expect(row).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(row);
    expect(onCollapse).toHaveBeenCalledWith({ alertname: 'Cardinality', source: 'yul' });
  });

  it('expands with the keyboard, since the row is the control', () => {
    const onExpand = vi.fn();
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={onExpand}
        onCollapse={noop}
        onLoadMoreChildren={noop}
      />,
    );
    fireEvent.keyDown(screen.getByRole('row', { name: /Cardinality/ }), { key: 'Enter' });
    expect(onExpand).toHaveBeenCalled();
  });

  it('renders members in the operator’s own columns and opens one on click', () => {
    const onSelect = vi.fn();
    render(
      <AlertGroupTable
        groups={[group()]}
        children={children()}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        onSelect={onSelect}
      />,
    );

    expect(screen.getAllByRole('row', { name: /firing/ })).toHaveLength(2);
    fireEvent.click(screen.getByText('a'));
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: '1' }));
  });

  it('renders a single-member group as a plain row with no chevron', () => {
    const onExpand = vi.fn();
    render(
      <AlertGroupTable
        groups={[group({ total: 1, acknowledged: 0, severityCounts: { warning: 1 } })]}
        children={{}}
        onExpand={onExpand}
        onCollapse={noop}
        onLoadMoreChildren={noop}
      />,
    );

    const row = screen.getByRole('row', { name: /Cardinality/ });
    // Grouping must add no ceremony where there is nothing to collapse.
    expect(row).not.toHaveAttribute('aria-expanded');
    fireEvent.click(row);
    expect(onExpand).not.toHaveBeenCalled();
  });

  it('offers the rest of a partially loaded group', () => {
    const onLoadMore = vi.fn();
    render(
      <AlertGroupTable
        groups={[group()]}
        children={children({ nextCursor: 'next', total: 52 })}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={onLoadMore}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: /load 50 more/i }));
    expect(onLoadMore).toHaveBeenCalledWith({ alertname: 'Cardinality', source: 'yul' });
  });

  it('surfaces a failed member load with a retry', () => {
    const onLoadMore = vi.fn();
    render(
      <AlertGroupTable
        groups={[group()]}
        children={children({ alerts: [], error: new Error('boom'), total: 0 })}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={onLoadMore}
      />,
    );
    expect(screen.getByRole('alert')).toHaveTextContent(/could not load members: boom/i);
    fireEvent.click(screen.getByRole('button', { name: /retry/i }));
    expect(onLoadMore).toHaveBeenCalled();
  });

  it('marks up the hierarchy so it is not just indentation', () => {
    render(
      <AlertGroupTable
        groups={[group()]}
        children={children()}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
      />,
    );
    expect(screen.getByRole('treegrid')).toBeInTheDocument();
    expect(screen.getByRole('row', { name: /Cardinality · yul/ })).toHaveAttribute(
      'aria-level',
      '1',
    );
    expect(screen.getAllByRole('row', { name: /firing/ })[0]).toHaveAttribute('aria-level', '2');
  });
});
