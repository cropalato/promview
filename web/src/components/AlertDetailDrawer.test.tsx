import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { AlertDetailResult } from '../alerts/detail';
import type { AlertDetailState } from '../hooks/useAlertDetail';
import { AlertDetailDrawer } from './AlertDetailDrawer';

function detailResult(overrides: Record<string, unknown> = {}): AlertDetailResult {
  return {
    alert: {
      id: '42',
      fingerprint: 'fp-42',
      source: 'am-eu',
      status: 'firing',
      severity: 'critical',
      severityLabel: 'Critical',
      name: 'HighErrorRate',
      labels: { alertname: 'HighErrorRate', team: 'core' },
      annotations: { summary: 'Error rate above 5% for 10m' },
      startsAt: '2026-08-14T10:00:00Z',
      endsAt: null,
      generatorURL: 'http://prometheus/graph',
      externalURL: 'http://alertmanager',
      firstSeen: '2026-08-14T10:00:00Z',
      lastSeen: '2026-08-14T11:00:00Z',
      repeatCount: 3,
      occurrence: 2,
      rawData: { status: 'firing' },
      ...overrides,
    },
    history: [
      {
        id: 11,
        occurrence: 2,
        type: 'updated',
        typeLabel: 'Updated',
        sourceStatus: 'firing',
        actor: 'alertmanager',
        message: 'Notification sent to team-core',
        occurredAt: '2026-08-14T11:00:00Z',
      },
      {
        id: 10,
        occurrence: 2,
        type: 'created',
        typeLabel: 'Created',
        sourceStatus: 'firing',
        actor: 'alertmanager',
        message: '',
        occurredAt: '2026-08-14T10:00:00Z',
      },
    ],
  };
}

function renderDrawer(state: AlertDetailState, onClose = vi.fn(), onRetry = vi.fn()) {
  const utils = render(
    <AlertDetailDrawer alertId="42" state={state} onClose={onClose} onRetry={onRetry} />,
  );
  return { ...utils, onClose, onRetry };
}

describe('AlertDetailDrawer', () => {
  it('renders a labelled non-modal dialog with the alert name', () => {
    renderDrawer({ status: 'ready', detail: detailResult() });

    const dialog = screen.getByRole('dialog', { name: 'HighErrorRate' });
    expect(dialog).toHaveAttribute('aria-modal', 'false');
    expect(within(dialog).getByText('am-eu', { selector: '.detail-subtitle' })).toBeInTheDocument();
  });

  it('keeps a stable accessible name while loading, so the sheet semantics hold at any size', () => {
    renderDrawer({ status: 'loading' });

    const dialog = screen.getByRole('dialog', { name: 'Alert detail' });
    expect(dialog).toHaveAttribute('aria-modal', 'false');
    expect(within(dialog).getByRole('status')).toHaveTextContent(/loading alert detail/i);
  });

  it('moves focus into the dialog on open and restores it on close', () => {
    function Harness({ open }: { open: boolean }) {
      return (
        <>
          <button type="button">selected row</button>
          {open ? (
            <AlertDetailDrawer
              alertId="42"
              state={{ status: 'ready', detail: detailResult() }}
              onClose={vi.fn()}
              onRetry={vi.fn()}
            />
          ) : null}
        </>
      );
    }
    const { rerender } = render(<Harness open={false} />);
    const row = screen.getByRole('button', { name: 'selected row' });
    row.focus();

    rerender(<Harness open={true} />);
    expect(screen.getByRole('dialog')).toHaveFocus();

    rerender(<Harness open={false} />);
    expect(row).toHaveFocus();
  });

  it('closes via the close button and via Escape', () => {
    const { onClose } = renderDrawer({ status: 'ready', detail: detailResult() });

    fireEvent.click(screen.getByRole('button', { name: /close alert detail/i }));
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('ignores Escape when another handler already consumed it', () => {
    const { onClose } = renderDrawer({ status: 'ready', detail: detailResult() });

    const prevent = (event: globalThis.KeyboardEvent) => event.preventDefault();
    document.body.addEventListener('keydown', prevent);
    fireEvent.keyDown(document.body, { key: 'Escape' });
    document.body.removeEventListener('keydown', prevent);

    expect(onClose).not.toHaveBeenCalled();
  });

  it('switches tabs by click and by arrow keys with roving tabindex', () => {
    renderDrawer({ status: 'ready', detail: detailResult() });

    const overview = screen.getByRole('tab', { name: 'Overview' });
    const timeline = screen.getByRole('tab', { name: 'Timeline' });
    const raw = screen.getByRole('tab', { name: 'Raw' });
    expect(overview).toHaveAttribute('aria-selected', 'true');
    expect(overview).toHaveAttribute('tabindex', '0');
    expect(timeline).toHaveAttribute('tabindex', '-1');

    fireEvent.click(timeline);
    expect(timeline).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tabpanel', { name: 'Timeline' })).toHaveTextContent(
      'Notification sent to team-core',
    );

    fireEvent.keyDown(timeline, { key: 'ArrowRight' });
    expect(raw).toHaveAttribute('aria-selected', 'true');
    expect(raw).toHaveFocus();

    fireEvent.keyDown(raw, { key: 'ArrowLeft' });
    expect(timeline).toHaveAttribute('aria-selected', 'true');
    expect(timeline).toHaveFocus();

    fireEvent.keyDown(timeline, { key: 'End' });
    expect(raw).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(raw, { key: 'Home' });
    expect(overview).toHaveAttribute('aria-selected', 'true');
  });

  it('renders the raw tab as formatted JSON without interpreting HTML', () => {
    const { container } = renderDrawer({
      status: 'ready',
      detail: detailResult({ rawData: { note: '<img src=x onerror=alert(1)>' } }),
    });

    fireEvent.click(screen.getByRole('tab', { name: 'Raw' }));

    const pre = container.querySelector('.detail-raw-pre');
    expect(pre).not.toBeNull();
    expect(pre?.textContent).toContain('"note": "<img src=x onerror=alert(1)>"');
    expect(container.querySelector('img')).toBeNull();
  });

  it('shows the error state with a retry', () => {
    const { onRetry } = renderDrawer({
      status: 'error',
      error: new Error('Alert detail request failed (HTTP 503)'),
    });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(/cannot load alert detail/i);
    expect(alert).toHaveTextContent(/HTTP 503/);

    fireEvent.click(screen.getByRole('button', { name: /retry detail request/i }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('shows the not-found state with a retry', () => {
    const { onRetry } = renderDrawer({ status: 'not-found' });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(/alert not found/i);
    expect(alert).toHaveTextContent(/resolved and pruned/i);

    fireEvent.click(screen.getByRole('button', { name: /retry detail request/i }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
