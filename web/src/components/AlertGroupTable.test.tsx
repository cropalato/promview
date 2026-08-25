import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { AlertGroupSummary } from '../alerts/api';
import { resolveColumns } from '../alerts/columns';
import type { AlertSummary } from '../alerts/types';
import type { GroupChildren } from '../hooks/useGroupChildren';
import { AlertGroupTable } from './AlertGroupTable';

function group(overrides: Partial<AlertGroupSummary> = {}): AlertGroupSummary {
  return {
    key: { alertname: 'Cardinality', source: 'yul' },
    total: 52,
    acknowledged: 3,
    silenced: 0,
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
    silencedBy: [],
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

function getGroupRow(key: Record<string, string> = { alertname: 'Cardinality', source: 'yul' }) {
  const encodedKey = JSON.stringify(key);
  const row = screen
    .getAllByRole('row')
    .find((candidate) => candidate.getAttribute('data-group') === encodedKey);
  if (row === undefined) {
    throw new Error(`Could not find group row for ${encodedKey}`);
  }
  return row;
}

function getChildRow(alertId: string) {
  const row = screen
    .getAllByRole('row')
    .find((candidate) => candidate.getAttribute('data-alert-id') === alertId);
  if (row === undefined) {
    throw new Error(`Could not find child row for ${alertId}`);
  }
  return row;
}

function parentCell(element: HTMLElement): HTMLTableCellElement {
  const cell = element.closest('td');
  if (cell === null) {
    throw new Error('Could not find containing table cell');
  }
  return cell;
}

function cellForColumn(row: HTMLElement, label: string): HTMLElement {
  const index = screen
    .getAllByRole('columnheader')
    .findIndex((header) => header.textContent?.trim() === label);
  if (index === -1) {
    throw new Error(`Could not find ${label} column`);
  }
  const cell = row.children.item(index);
  if (cell === null) {
    throw new Error(`Could not find ${label} cell`);
  }
  return cell as HTMLElement;
}

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

  it('uses one group-control column and keeps aggregates inside it', () => {
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
      />,
    );

    const row = getGroupRow();
    const control = row.children.item(0) as HTMLElement;
    expect(screen.getAllByRole('columnheader')).toHaveLength(12);
    expect(screen.getByRole('columnheader', { name: 'Alert group' })).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Group summary' })).not.toBeInTheDocument();
    expect(screen.getByRole('treegrid').querySelectorAll('colgroup col')).toHaveLength(12);

    // The row header holds the tree control and group aggregate; normal
    // columns start immediately after it.
    expect(control).toHaveTextContent('Critical');
    expect(control).toHaveTextContent('52');
    expect(within(control).getByText('51 warning')).toBeInTheDocument();
    expect(within(control).getByText('3/52')).toBeInTheDocument();
    expect(control).not.toHaveTextContent('Cardinality');
    expect(control).not.toHaveTextContent('yul');

    expect(row.children).toHaveLength(12);
    expect(cellForColumn(row, 'Alert')).toHaveTextContent('Cardinality');
    expect(cellForColumn(row, 'Source')).toHaveTextContent('yul');
    expect(cellForColumn(row, 'Severity')).toBeEmptyDOMElement();
  });

  it('spans the one control and selected normal columns for an empty result', () => {
    render(
      <AlertGroupTable
        groups={[]}
        children={{}}
        columns={resolveColumns(['source', 'alert'])}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
      />,
    );

    expect(screen.getAllByRole('columnheader')).toHaveLength(3);
    expect(parentCell(screen.getByText('All clear — no active alerts'))).toHaveAttribute(
      'colspan',
      '3',
    );
  });

  it('places selected normal columns directly after the control in their saved order', () => {
    const key = {
      alertname: 'Cardinality',
      source: 'yul',
      team: 'platform',
      instance: 'db-1',
      severity: 'warning',
    };
    render(
      <AlertGroupTable
        groups={[
          group({
            key,
            severityCounts: { warning: 52 },
            worstSeverity: 'warning',
          }),
        ]}
        children={{}}
        columns={resolveColumns([
          'label:alertname',
          'source',
          'instance',
          'severity',
          'team',
          'alert',
          'label:source',
        ])}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
      />,
    );

    const row = getGroupRow(key);
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent?.trim())).toEqual(
      ['Alert group', 'alertname', 'Source', 'Instance', 'Severity', 'Team', 'Alert', 'source'],
    );
    expect(row.children).toHaveLength(8);
    expect(cellForColumn(row, 'Alert')).toHaveTextContent('Cardinality');
    expect(cellForColumn(row, 'Source')).toHaveTextContent('yul');
    expect(cellForColumn(row, 'Team')).toHaveTextContent('platform');
    expect(cellForColumn(row, 'Instance')).toHaveTextContent('db-1');
    expect(cellForColumn(row, 'Severity')).toHaveTextContent('warning');
    expect(cellForColumn(row, 'alertname')).toHaveTextContent('Cardinality');
    expect(cellForColumn(row, 'source')).toHaveTextContent('yul');
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
    const row = getGroupRow();
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
    const firstChild = getChildRow('1');
    expect(firstChild.children).toHaveLength(12);
    expect(firstChild.children.item(0)).toBeEmptyDOMElement();
    expect(cellForColumn(firstChild, 'Alert')).toHaveTextContent('Cardinality');
    fireEvent.click(screen.getByText('a'));
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: '1' }));
  });

  it('aligns child and member-load status rows to one control and selected columns', () => {
    const columns = resolveColumns(['source', 'alert', 'summary']);
    const props = {
      groups: [group()],
      columns,
      onExpand: noop,
      onCollapse: noop,
      onLoadMoreChildren: noop,
    };
    const { rerender } = render(
      <AlertGroupTable {...props} children={children({ loading: true })} />,
    );

    const child = getChildRow('1');
    expect(child.children).toHaveLength(4);
    expect(child.children.item(0)).toBeEmptyDOMElement();
    expect(cellForColumn(child, 'Source')).toHaveTextContent('yul');
    expect(cellForColumn(child, 'Alert')).toHaveTextContent('Cardinality');
    expect(cellForColumn(child, 'Summary')).toHaveTextContent('too many series');
    expect(parentCell(screen.getByText('Loading members…'))).toHaveAttribute('colspan', '4');

    rerender(
      <AlertGroupTable
        {...props}
        children={children({ alerts: [], error: new Error('boom'), total: 0 })}
      />,
    );
    expect(parentCell(screen.getByRole('alert'))).toHaveAttribute('colspan', '4');

    rerender(
      <AlertGroupTable
        {...props}
        children={children({ alerts: [], nextCursor: 'next', total: 52 })}
      />,
    );
    expect(parentCell(screen.getByRole('button', { name: /load 52 more/i }))).toHaveAttribute(
      'colspan',
      '4',
    );
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
    const control = row.children.item(0) as HTMLElement;
    // Grouping must add no ceremony where there is nothing to collapse, and
    // with no open handler the row stays inert rather than half-interactive.
    expect(row).not.toHaveAttribute('aria-expanded');
    expect(row).not.toHaveAttribute('tabindex');
    expect(row.children).toHaveLength(12);
    expect(within(control).getByText('1 warning')).toBeInTheDocument();
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

    const row = getGroupRow();
    // Summary, instance and team come from the member, not from an aggregate.
    expect(row.children).toHaveLength(12);
    expect(cellForColumn(row, 'Summary')).toHaveTextContent('too many series');
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

    const row = getGroupRow();
    const control = row.children.item(0) as HTMLElement;
    expect(within(control).getByText('1 warning')).toBeInTheDocument();
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

    const row = getGroupRow();
    const control = row.children.item(0) as HTMLElement;
    expect(within(control).getByText('51 warning')).toBeInTheDocument();
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
    expect(getGroupRow()).toHaveAttribute('aria-level', '1');
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

    // Every operator column carries a handle; the group control stays fixed.
    expect(screen.getAllByRole('separator')).toHaveLength(11);
    expect(screen.getByRole('separator', { name: 'Resize Severity column' })).toBeInTheDocument();
    expect(
      screen.queryByRole('separator', { name: 'Resize Alert group column' }),
    ).not.toBeInTheDocument();

    // The one group-control column precedes the operator columns, which retain
    // their own width and order in a grouped view.
    const cols = container.querySelectorAll('colgroup col');
    expect(cols).toHaveLength(12);
    expect(cols[2]).toHaveStyle({ width: '96px' });
    expect(cols[4]).toHaveStyle({ width: '300px' });

    fireEvent.keyDown(screen.getByRole('separator', { name: 'Resize Summary column' }), {
      key: 'ArrowRight',
    });
    expect(onColumnResize).toHaveBeenCalledWith('summary', 316);
  });

  it('keeps the group control independent from normal column widths', () => {
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

    // The control owns only the first column. Severity still starts from its
    // registry basis, while Alert remains flexible.
    let cols = container.querySelectorAll('colgroup col');
    expect(cols).toHaveLength(12);
    expect(cols[1]).toHaveStyle({ width: '116px' });
    expect(cols[3]).not.toHaveAttribute('style');

    // A stored normal-column width still applies without changing the control.
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
    expect(cols[1]).toHaveStyle({ width: '260px' });
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

  it('renders sort buttons on normal sortable columns, but never on the group control', () => {
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

    const sortable = ['Severity', 'State', 'Alert', 'Summary', 'Team', 'Instance', 'Source', 'Age'];
    for (const label of sortable) {
      expect(screen.getByRole('button', { name: `Sort by ${label}` })).toBeInTheDocument();
    }
    // The group control is not an alert field, so it gets no control; nor do
    // the unsortable normal columns.
    expect(screen.queryByRole('button', { name: /alert group/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /last seen/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /assignee/i })).not.toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: /alert group/i })).not.toHaveAttribute(
      'aria-sort',
    );
    expect(screen.queryByRole('columnheader', { name: /group summary/i })).not.toBeInTheDocument();
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

  it('keeps the resize handle on every normal header alongside the sort controls', () => {
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

    // Eleven normal columns, eleven handles — the sort button did not displace them.
    expect(screen.getAllByRole('separator')).toHaveLength(11);
    const summaryHeader = screen.getByRole('button', { name: 'Sort by Summary' }).closest('th');
    expect(
      within(summaryHeader as HTMLElement).getByRole('separator', {
        name: 'Resize Summary column',
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole('separator', { name: 'Resize Severity column' })).toBeInTheDocument();
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
  it('explains the silence control, which carries no words of its own', () => {
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={noop}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        onSilenceGroup={vi.fn()}
      />,
    );

    const control = screen.getByRole('button', {
      name: 'Silence alertname=Cardinality, source=yul',
    });
    // A glyph nobody has met teaches nobody on its own. The hover text has to
    // say what silencing does rather than name the verb again, and the button
    // must keep an accessible name now that the word is gone from it.
    expect(control).toHaveAttribute(
      'title',
      'Silence alertname=Cardinality, source=yul — stop its alerts notifying for a set time',
    );
    expect(control).toHaveTextContent('');
  });

  it('silences a group without expanding the row it sits in', () => {
    const onSilenceGroup = vi.fn();
    const onExpand = vi.fn();
    render(
      <AlertGroupTable
        groups={[group()]}
        children={{}}
        onExpand={onExpand}
        onCollapse={noop}
        onLoadMoreChildren={noop}
        onSilenceGroup={onSilenceGroup}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: /^Silence / }));
    expect(onSilenceGroup).toHaveBeenCalledTimes(1);
    // Silencing a group and opening it are different intentions, and the click
    // lands on both without the handler stopping it.
    expect(onExpand).not.toHaveBeenCalled();
  });
});

function renderGroups(groups: AlertGroupSummary[]) {
  render(
    <AlertGroupTable
      groups={groups}
      children={{}}
      onExpand={noop}
      onCollapse={noop}
      onLoadMoreChildren={noop}
    />,
  );
}

describe('silenced groups', () => {
  it('says how much of a group is being held back', () => {
    renderGroups([group({ total: 52, silenced: 7 })]);
    // Without the fraction a mostly-silenced group reads exactly like a
    // mostly-firing one, which is the pair an operator has to tell apart.
    expect(screen.getByText('7/52 silenced')).toBeInTheDocument();
  });

  it('marks a wholly silenced group without hiding it', () => {
    renderGroups([group({ total: 4, silenced: 4 })]);
    expect(screen.getByText('silenced')).toBeInTheDocument();
    expect(screen.getByText('Cardinality')).toBeInTheDocument();
  });

  it('stays out of the way when nothing is silenced', () => {
    renderGroups([group({ silenced: 0 })]);
    expect(screen.queryByText(/silenced/)).not.toBeInTheDocument();
  });
});
