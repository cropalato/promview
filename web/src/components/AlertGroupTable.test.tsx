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
    const row = screen.getByRole('row', { name: /alertname=Cardinality · source=yul/ });
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
    // Grouping must add no ceremony where there is nothing to collapse, and
    // with no open handler the row stays inert rather than half-interactive.
    expect(row).not.toHaveAttribute('aria-expanded');
    expect(row).not.toHaveAttribute('tabindex');
    fireEvent.click(row);
    expect(onExpand).not.toHaveBeenCalled();
  });

  it('opens a single-member group’s only alert directly on click', () => {
    const onExpand = vi.fn();
    const onOpenAlert = vi.fn();
    render(
      <AlertGroupTable
        groups={[group({ total: 1, acknowledged: 0, severityCounts: { warning: 1 } })]}
        children={{}}
        onExpand={onExpand}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        onOpenAlert={onOpenAlert}
      />,
    );

    const row = screen.getByRole('row', { name: /Cardinality/ });
    // No expansion ceremony, but the row is the control that opens the alert.
    expect(row).not.toHaveAttribute('aria-expanded');
    expect(row).toHaveAttribute('tabindex', '0');
    fireEvent.click(row);
    expect(onOpenAlert).toHaveBeenCalledWith('42');
    expect(onExpand).not.toHaveBeenCalled();
  });

  it('opens a single-member group’s alert from the keyboard', () => {
    const onOpenAlert = vi.fn();
    render(
      <AlertGroupTable
        groups={[group({ total: 1, acknowledged: 0, severityCounts: { warning: 1 } })]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        onOpenAlert={onOpenAlert}
      />,
    );

    fireEvent.keyDown(screen.getByRole('row', { name: /Cardinality/ }), { key: 'Enter' });
    expect(onOpenAlert).toHaveBeenCalledWith('42');
  });

  it('expands a multi-member group instead of opening its sample alert', () => {
    const onExpand = vi.fn();
    const onOpenAlert = vi.fn();
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={onExpand}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        onOpenAlert={onOpenAlert}
      />,
    );

    fireEvent.click(screen.getByRole('row', { name: /Cardinality/ }));
    expect(onExpand).toHaveBeenCalledWith({ alertname: 'Cardinality', source: 'yul' });
    expect(onOpenAlert).not.toHaveBeenCalled();
  });

  it('fills a single-member group’s columns from the member alert', () => {
    const only = member('42', 'db-1');
    render(
      <AlertGroupTable
        groups={[group({ total: 1, acknowledged: 0, severityCounts: { warning: 1 } })]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        memberFor={(id) => (id === '42' ? only : undefined)}
      />,
    );

    const row = screen.getByRole('row', { name: /alertname=Cardinality · source=yul/ });
    // Summary, instance and team come from the member, not from an aggregate.
    expect(within(row).getByText('too many series')).toBeInTheDocument();
    expect(within(row).getByText('db-1')).toBeInTheDocument();
    expect(within(row).getByText('platform')).toBeInTheDocument();
    // The severity mix is the multi-member summary; one member adds nothing.
    expect(within(row).queryByText('1 warning')).not.toBeInTheDocument();
  });

  it('keeps the aggregate summary for a single member that is not loaded', () => {
    render(
      <AlertGroupTable
        groups={[group({ total: 1, acknowledged: 0, severityCounts: { warning: 1 } })]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        memberFor={() => undefined}
      />,
    );

    const row = screen.getByRole('row', { name: /alertname=Cardinality · source=yul/ });
    expect(within(row).getByText('1 warning')).toBeInTheDocument();
  });

  it('keeps the multi-member summary even when the sample alert is loaded', () => {
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        memberFor={() => member('42', 'a')}
      />,
    );

    const row = screen.getByRole('row', { name: /alertname=Cardinality · source=yul/ });
    expect(within(row).getByText('51 warning')).toBeInTheDocument();
    expect(within(row).queryByText('too many series')).not.toBeInTheDocument();
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
    expect(screen.getByRole('row', { name: /alertname=Cardinality · source=yul/ })).toHaveAttribute(
      'aria-level',
      '1',
    );
    expect(screen.getAllByRole('row', { name: /firing/ })[0]).toHaveAttribute('aria-level', '2');
  });

  it('shares the flat table widths and handles, keyed by column id', () => {
    const onColumnResize = vi.fn();
    const { container } = render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        columnWidths={{ state: 96, summary: 300 }}
        onColumnResize={onColumnResize}
        onColumnResizeReset={noop}
      />,
    );

    // Every header carries a handle, the heading column included.
    expect(screen.getAllByRole('separator')).toHaveLength(11);
    expect(
      screen.getByRole('separator', { name: 'Resize Alert group column' }),
    ).toBeInTheDocument();

    // The heading column stands in for the first child column (severity), so
    // the width for state lands on the second col, as in the flat table.
    const cols = container.querySelectorAll('colgroup col');
    expect(cols).toHaveLength(11);
    expect(cols[1]).toHaveStyle({ width: '96px' });
    expect(cols[3]).toHaveStyle({ width: '300px' });

    fireEvent.keyDown(screen.getByRole('separator', { name: 'Resize Summary column' }), {
      key: 'ArrowRight',
    });
    expect(onColumnResize).toHaveBeenCalledWith('summary', 316);
  });

  it('lets the heading column flex by default but honors a stored first-column width', () => {
    const { container, rerender } = render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        onColumnResize={noop}
        onColumnResizeReset={noop}
      />,
    );

    // The heading renders far more than the severity tag the first column's
    // basis is sized for, so by default it carries no width and shares the
    // leftover with the long-text columns. The other based columns keep
    // their registry defaults.
    let cols = container.querySelectorAll('colgroup col');
    expect(cols[0]).not.toHaveAttribute('style');
    expect(cols[1]).toHaveStyle({ width: '116px' });

    // A stored width for the first column (severity) still applies here.
    rerender(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        columnWidths={{ severity: 260 }}
        onColumnResize={noop}
        onColumnResizeReset={noop}
      />,
    );
    cols = container.querySelectorAll('colgroup col');
    expect(cols[0]).toHaveStyle({ width: '260px' });
  });
});

describe('AlertGroupTable sortable headers', () => {
  it('renders plain headers when no sort handler is provided', () => {
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
      />,
    );

    expect(screen.queryByRole('button', { name: /sort by/i })).not.toBeInTheDocument();
    for (const header of screen.getAllByRole('columnheader')) {
      expect(header).not.toHaveAttribute('aria-sort');
    }
  });

  it('renders sort buttons on the sortable columns, but never on the group heading', () => {
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        sort={null}
        onSortChange={vi.fn()}
      />,
    );

    // The heading replaces the severity column, so the sortable set is the
    // flat table's minus severity.
    const sortable = ['State', 'Alert', 'Summary', 'Team', 'Instance', 'Source', 'Age'];
    for (const label of sortable) {
      expect(screen.getByRole('button', { name: `Sort by ${label}` })).toBeInTheDocument();
    }
    // The heading renders the group key, not an alert field, so it gets no
    // control; nor do the unsortable columns.
    expect(screen.queryByRole('button', { name: /alert group/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /severity/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /last seen/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /assignee/i })).not.toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: /alert group/i })).not.toHaveAttribute(
      'aria-sort',
    );
    // No column is sorted yet.
    for (const header of screen.getAllByRole('columnheader')) {
      expect(header).not.toHaveAttribute('aria-sort');
    }
  });

  it('requests an ascending sort for an inactive column', () => {
    const onSortChange = vi.fn();
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        sort={null}
        onSortChange={onSortChange}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sort by Age' }));
    expect(onSortChange).toHaveBeenCalledWith({ field: 'age', order: 'asc' });

    // The Alert column sorts by the alert name server-side.
    fireEvent.click(screen.getByRole('button', { name: 'Sort by Alert' }));
    expect(onSortChange).toHaveBeenCalledWith({ field: 'name', order: 'asc' });
  });

  it('marks the sorted column with aria-sort and toggles its direction', () => {
    const onSortChange = vi.fn();
    const { rerender } = render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        sort={{ field: 'team', order: 'asc' }}
        onSortChange={onSortChange}
      />,
    );

    const headerOf = (label: string) =>
      screen.getByRole('button', { name: `Sort by ${label}` }).closest('th');
    expect(headerOf('Team')).toHaveAttribute('aria-sort', 'ascending');
    expect(headerOf('Age')).not.toHaveAttribute('aria-sort');

    fireEvent.click(screen.getByRole('button', { name: 'Sort by Team' }));
    expect(onSortChange).toHaveBeenCalledWith({ field: 'team', order: 'desc' });

    rerender(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        sort={{ field: 'team', order: 'desc' }}
        onSortChange={onSortChange}
      />,
    );
    expect(headerOf('Team')).toHaveAttribute('aria-sort', 'descending');
  });

  it('keeps the resize handle on every header alongside the sort controls', () => {
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        sort={null}
        onSortChange={vi.fn()}
        onColumnResize={noop}
        onColumnResizeReset={noop}
      />,
    );

    // Eleven columns, eleven handles — the sort button did not displace them.
    expect(screen.getAllByRole('separator')).toHaveLength(11);
    const summaryHeader = screen.getByRole('button', { name: 'Sort by Summary' }).closest('th');
    expect(
      within(summaryHeader as HTMLElement).getByRole('separator', {
        name: 'Resize Summary column',
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('separator', { name: 'Resize Alert group column' }),
    ).toBeInTheDocument();
  });

  it('still opens a single-member group’s alert directly when sorting is wired', () => {
    const onExpand = vi.fn();
    const onOpenAlert = vi.fn();
    render(
      <AlertGroupTable
        groups={[group({ total: 1, acknowledged: 0, severityCounts: { warning: 1 } })]}
        children={{}}
        onExpand={onExpand}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        sort={null}
        onSortChange={vi.fn()}
        onOpenAlert={onOpenAlert}
      />,
    );

    const row = screen.getByRole('row', { name: /Cardinality/ });
    expect(row).not.toHaveAttribute('aria-expanded');
    fireEvent.click(row);
    expect(onOpenAlert).toHaveBeenCalledWith('42');
    expect(onExpand).not.toHaveBeenCalled();
  });
});
