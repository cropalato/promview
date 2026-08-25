import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SilenceDialog } from './SilenceDialog';
import type { SilenceResponse } from '../alerts/silence';

function ok(results: SilenceResponse['results']): SilenceResponse {
  return { endsAt: '2026-08-21T16:00:00Z', createdBy: 'ada@example.com', results };
}

function renderDialog(overrides: Partial<Parameters<typeof SilenceDialog>[0]> = {}) {
  const onConfirm = vi
    .fn()
    .mockResolvedValue(
      ok([{ source: 'demo', silenceId: 'abc', matchers: { alertname: 'HighCPU' }, members: 1 }]),
    );
  const onClose = vi.fn();
  render(
    <SilenceDialog
      subject="HighCPU"
      matchers={{ alertname: 'HighCPU', instance: 'web-01' }}
      memberCount={1}
      defaultSeconds={7200}
      maxSeconds={30 * 24 * 60 * 60}
      onConfirm={onConfirm}
      onClose={onClose}
      {...overrides}
    />,
  );
  return { onConfirm, onClose };
}

describe('SilenceDialog', () => {
  it('shows the exact matchers, not a description of them', () => {
    // This is the part an operator can actually check before hiding alerts.
    renderDialog();
    expect(screen.getByText('alertname')).toBeInTheDocument();
    expect(screen.getByText('HighCPU')).toBeInTheDocument();
    expect(screen.getByText('instance')).toBeInTheDocument();
    expect(screen.getByText('web-01')).toBeInTheDocument();
  });

  it('defaults to the deployment window and confirms with it', async () => {
    const { onConfirm } = renderDialog();
    expect(screen.getByLabelText('Duration')).toHaveValue('7200');

    fireEvent.click(screen.getByRole('button', { name: 'Silence' }));
    // No resolve step ran, so there is no server-resolved match to echo back.
    await waitFor(() => expect(onConfirm).toHaveBeenCalledWith(7200, '', undefined));
  });

  it('sends the chosen window and comment', async () => {
    const { onConfirm } = renderDialog();
    fireEvent.change(screen.getByLabelText('Duration'), { target: { value: '1800' } });
    fireEvent.change(screen.getByLabelText('Comment'), { target: { value: '  db upgrade  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Silence' }));

    await waitFor(() => expect(onConfirm).toHaveBeenCalledWith(1800, 'db upgrade', undefined));
  });

  it('offers no window past the deployment maximum', () => {
    renderDialog({ defaultSeconds: 3600, maxSeconds: 4 * 60 * 60 });
    expect(screen.queryByRole('option', { name: '1 day' })).not.toBeInTheDocument();
    expect(screen.getByRole('option', { name: '4 hours' })).toBeInTheDocument();
  });

  it('reports a partial application per Alertmanager rather than as done', async () => {
    const onConfirm = vi.fn().mockResolvedValue(
      ok([
        { source: 'demo', silenceId: 'abc', matchers: { alertname: 'HighCPU' }, members: 5 },
        { source: 'edge', error: 'HTTP 401', matchers: { alertname: 'HighCPU' }, members: 4 },
      ]),
    );
    render(
      <SilenceDialog
        subject="HighCPU"
        matchers={{ alertname: 'HighCPU' }}
        memberCount={9}
        defaultSeconds={7200}
        maxSeconds={7 * 24 * 60 * 60}
        onConfirm={onConfirm}
        onClose={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Silence' }));

    // An operator told "done" while half the group still fires is worse off
    // than one told which half failed.
    expect(await screen.findByText(/HTTP 401/)).toBeInTheDocument();
    expect(screen.getByText('edge')).toBeInTheDocument();
    expect(screen.getByText(/1 of 2 Alertmanagers accepted/)).toBeInTheDocument();
  });

  it('keeps the dialog open and retryable when the request fails', async () => {
    const onConfirm = vi.fn().mockRejectedValue(new Error('operator access required'));
    render(
      <SilenceDialog
        subject="HighCPU"
        matchers={{ alertname: 'HighCPU' }}
        defaultSeconds={7200}
        maxSeconds={7 * 24 * 60 * 60}
        onConfirm={onConfirm}
        onClose={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Silence' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('operator access required');
    expect(screen.getByRole('button', { name: 'Silence' })).toBeEnabled();
  });

  it('closes on cancel and on Escape without silencing anything', () => {
    const { onConfirm, onClose } = renderDialog();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(2);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('is a modal dialog with an accessible name', () => {
    renderDialog();
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName(/HighCPU/);
  });
});

describe('resolving what a group silence actually matches', () => {
  it('replaces the grouping key with the match the server resolved', async () => {
    const resolve = vi.fn().mockResolvedValue({
      matchers: { alertname: 'HighCPU', cluster: 'prod', team: 'platform' },
      memberCount: 12,
      targets: [
        {
          source: 'demo',
          matchers: { alertname: 'HighCPU', cluster: 'prod', team: 'platform' },
          members: 12,
        },
      ],
    });
    const onConfirm = vi.fn().mockResolvedValue(ok([]));
    render(
      <SilenceDialog
        subject="HighCPU"
        matchers={{ alertname: 'HighCPU' }}
        memberCount={12}
        resolve={resolve}
        defaultSeconds={7200}
        maxSeconds={30 * 24 * 60 * 60}
        onConfirm={onConfirm}
        onClose={vi.fn()}
      />,
    );

    // Silencing on `alertname` alone would hide the same rule for every
    // cluster and team, including alerts nobody has seen yet.
    await waitFor(() => expect(screen.getByText('cluster')).toBeInTheDocument());
    expect(screen.getByText('prod')).toBeInTheDocument();
    expect(screen.getByText('platform')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Silence' }));
    await waitFor(() =>
      expect(onConfirm).toHaveBeenCalledWith(7200, '', {
        alertname: 'HighCPU',
        cluster: 'prod',
        team: 'platform',
      }),
    );
  });

  it('spells out the Alertmanagers whose match is narrower still', async () => {
    const resolve = vi.fn().mockResolvedValue({
      matchers: { alertname: 'HighCPU' },
      memberCount: 5,
      targets: [
        { source: 'demo', matchers: { alertname: 'HighCPU', cluster: 'a' }, members: 3 },
        { source: 'edge', matchers: { alertname: 'HighCPU', cluster: 'b' }, members: 2 },
      ],
    });
    render(
      <SilenceDialog
        subject="HighCPU"
        matchers={{ alertname: 'HighCPU' }}
        resolve={resolve}
        defaultSeconds={7200}
        maxSeconds={30 * 24 * 60 * 60}
        onConfirm={vi.fn().mockResolvedValue(ok([]))}
        onClose={vi.fn()}
      />,
    );

    // Showing only the common match would name a broader scope than what
    // actually gets written to each Alertmanager.
    await waitFor(() => expect(screen.getByText(/cluster="a"/)).toBeInTheDocument());
    expect(screen.getByText(/cluster="b"/)).toBeInTheDocument();
  });

  it('will not let a silence be confirmed while the match is still unknown', () => {
    render(
      <SilenceDialog
        subject="HighCPU"
        matchers={{ alertname: 'HighCPU' }}
        resolve={() => new Promise(() => {})}
        defaultSeconds={7200}
        maxSeconds={30 * 24 * 60 * 60}
        onConfirm={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByRole('button', { name: 'Silence' })).toBeDisabled();
  });
});
