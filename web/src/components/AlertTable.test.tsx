import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { AlertSummary } from '../alerts/types';
import { AlertTable } from './AlertTable';
import type { AlertPagination } from './AlertTable';

const firingAlert: AlertSummary = {
  id: 'a1',
  severity: 'critical',
  state: 'firing',
  name: 'HighErrorRate',
  summary: 'Error rate above 5% for 10m',
  team: 'core',
  instance: 'api-1:9090',
  source: 'am-eu',
  startsAt: new Date(Date.now() - 5 * 60_000).toISOString(),
  notes: 2,
};

function pagination(overrides: Partial<AlertPagination> = {}): AlertPagination {
  return {
    loaded: 1,
    total: 3,
    hasMore: true,
    loadingMore: false,
    error: null,
    onLoadMore: vi.fn(),
    ...overrides,
  };
}

describe('AlertTable', () => {
  it('renders the default operational columns', () => {
    render(<AlertTable alerts={[]} />);

    const headers = screen.getAllByRole('columnheader').map((header) => header.textContent);
    expect(headers).toEqual([
      'Severity',
      'State',
      'Alert',
      'Summary',
      'Team',
      'Instance',
      'Source',
      'Age',
      'Assignee',
      'Notes',
    ]);
    expect(screen.getByText('Active alerts (0)')).toBeInTheDocument();
  });

  it('shows a meaningful empty state with ingestion guidance', () => {
    render(<AlertTable alerts={[]} />);

    expect(screen.getByRole('heading', { name: /all clear/i })).toBeInTheDocument();
    expect(screen.getByText(/alertmanager sources will stream/i)).toBeInTheDocument();
    expect(screen.getByText(/POST \/api\/v1\/ingest\/alertmanager/i)).toBeInTheDocument();
  });

  it('renders rows when alerts are provided', () => {
    render(<AlertTable alerts={[firingAlert]} />);

    expect(screen.getByText('HighErrorRate')).toBeInTheDocument();
    expect(screen.getByText('Critical')).toBeInTheDocument();
    expect(screen.getByText('firing')).toBeInTheDocument();
    expect(screen.getByText('5m')).toBeInTheDocument();
    expect(screen.getByText('am-eu')).toBeInTheDocument();
    expect(screen.getByText('Active alerts (1)')).toBeInTheDocument();
  });

  it('renders resolved alerts with the resolved state chip', () => {
    render(<AlertTable alerts={[{ ...firingAlert, id: 'a2', state: 'resolved' }]} />);

    expect(screen.getByText('resolved')).toBeInTheDocument();
  });

  it('preserves the source text of unknown severities on the info styling', () => {
    render(
      <AlertTable
        alerts={[{ ...firingAlert, id: 'a3', severity: 'info', severityLabel: 'page' }]}
      />,
    );

    expect(screen.getByText('page')).toBeInTheDocument();
  });

  it('offers to clear an active filter', () => {
    const onClear = vi.fn();
    render(<AlertTable alerts={[]} filterActive filterQuery="critical" onClearFilter={onClear} />);

    expect(screen.getByRole('heading', { name: /no alerts match/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /clear filter/i }));
    expect(onClear).toHaveBeenCalledTimes(1);
  });

  it('reports loaded rows against the server total and loads more', () => {
    const page = pagination();
    render(<AlertTable alerts={[firingAlert]} pagination={page} />);

    expect(screen.getByText(/showing 1 of 3 firing alerts/i)).toBeInTheDocument();
    expect(screen.getByText(/filter matches loaded rows only/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /load more/i }));
    expect(page.onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('disables the load-more button while the next page is in flight', () => {
    render(<AlertTable alerts={[firingAlert]} pagination={pagination({ loadingMore: true })} />);

    expect(screen.getByRole('button', { name: /loading…/i })).toBeDisabled();
  });

  it('keeps the retry available when loading more fails', () => {
    const page = pagination({ error: new Error('Alerts request failed (HTTP 500)') });
    render(<AlertTable alerts={[firingAlert]} pagination={page} />);

    expect(screen.getByRole('alert')).toHaveTextContent(/could not load more alerts/i);
    fireEvent.click(screen.getByRole('button', { name: /retry next page/i }));
    expect(page.onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('omits the load-more button when the cursor is exhausted', () => {
    render(
      <AlertTable
        alerts={[firingAlert]}
        pagination={pagination({ hasMore: false, loaded: 3, total: 3 })}
      />,
    );

    expect(screen.getByText(/showing 3 of 3 firing alerts/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /load more/i })).not.toBeInTheDocument();
  });

  it('makes rows keyboard-accessible and activates them by click and Enter', () => {
    const onSelect = vi.fn();
    render(<AlertTable alerts={[firingAlert]} selectedId={null} onSelect={onSelect} />);

    const row = screen.getByRole('row', { name: /HighErrorRate/ });
    expect(row).toHaveAttribute('tabindex', '0');
    expect(row).toHaveAttribute('aria-selected', 'false');

    fireEvent.click(row);
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(firingAlert);

    fireEvent.keyDown(row, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledTimes(2);

    // Other keys do not activate the row.
    fireEvent.keyDown(row, { key: 'ArrowDown' });
    expect(onSelect).toHaveBeenCalledTimes(2);
  });

  it('marks the selected row', () => {
    render(<AlertTable alerts={[firingAlert]} selectedId="a1" onSelect={vi.fn()} />);

    expect(screen.getByRole('row', { name: /HighErrorRate/ })).toHaveAttribute(
      'aria-selected',
      'true',
    );
  });

  it('keeps rows inert when no selection handler is provided', () => {
    render(<AlertTable alerts={[firingAlert]} />);

    const row = screen.getByRole('row', { name: /HighErrorRate/ });
    expect(row).not.toHaveAttribute('tabindex');
    expect(row).not.toHaveAttribute('aria-selected');
  });
});
