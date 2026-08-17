import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AlertDetail } from '../alerts/detail';
import { AlertDetailOverview } from './AlertDetailOverview';

function detail(overrides: Partial<AlertDetail> = {}): AlertDetail {
  return {
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
    acknowledged: false,
    acknowledgedBy: '',
    acknowledgedAt: null,
    actions: { canAcknowledge: false },
    rawData: {},
    ...overrides,
  };
}

const clipboardDescriptor = Object.getOwnPropertyDescriptor(window.navigator, 'clipboard');

function stubClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(window.navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
  });
  return writeText;
}

afterEach(() => {
  if (clipboardDescriptor !== undefined) {
    Object.defineProperty(window.navigator, 'clipboard', clipboardDescriptor);
  } else {
    Reflect.deleteProperty(window.navigator, 'clipboard');
  }
});

describe('AlertDetailOverview', () => {
  it('shows the operational facts and timestamps', () => {
    render(<AlertDetailOverview detail={detail()} />);

    expect(screen.getByText('Critical')).toBeInTheDocument();
    expect(screen.getByText('firing')).toBeInTheDocument();
    expect(screen.getByText('am-eu')).toBeInTheDocument();
    expect(screen.getByText('fp-42')).toBeInTheDocument();

    const facts = screen.getByText('Occurrence').parentElement;
    expect(facts).toHaveTextContent('2');
    expect(screen.getByText('Repeat count').parentElement).toHaveTextContent('3');

    // Started and First seen share the fixture timestamp.
    expect(screen.getAllByText('2026-08-14 10:00:00 UTC')).toHaveLength(2);
    expect(screen.getByText('Ongoing')).toBeInTheDocument();
    expect(screen.getByText('2026-08-14 11:00:00 UTC')).toBeInTheDocument();
  });

  it('renders the end timestamp for resolved alerts', () => {
    render(
      <AlertDetailOverview
        detail={detail({ status: 'resolved', endsAt: '2026-08-14T12:30:00Z' })}
      />,
    );

    expect(screen.getByText('resolved')).toBeInTheDocument();
    expect(screen.getByText('2026-08-14 12:30:00 UTC')).toBeInTheDocument();
  });

  it('renders labels and annotations with copy controls', async () => {
    const writeText = stubClipboard();
    render(<AlertDetailOverview detail={detail()} />);

    expect(screen.getByText('alertname')).toBeInTheDocument();
    expect(screen.getByText('HighErrorRate')).toBeInTheDocument();
    expect(screen.getByText('summary')).toBeInTheDocument();
    expect(screen.getByText('Error rate above 5% for 10m')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Copy team' }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith('core'));
    expect(await screen.findByText('Copied')).toBeInTheDocument();
  });

  it('notes when labels or annotations are empty', () => {
    render(<AlertDetailOverview detail={detail({ labels: {}, annotations: {} })} />);

    expect(screen.getByText('No labels.')).toBeInTheDocument();
    expect(screen.getByText('No annotations.')).toBeInTheDocument();
  });

  it('links safe external URLs in a new tab', () => {
    render(<AlertDetailOverview detail={detail()} />);

    const generator = screen.getByRole('link', { name: /prometheus\/graph/ });
    expect(generator).toHaveAttribute('href', 'http://prometheus/graph');
    expect(generator).toHaveAttribute('target', '_blank');
    expect(generator).toHaveAttribute('rel', expect.stringContaining('noopener'));
    expect(screen.getByRole('link', { name: /alertmanager/ })).toHaveAttribute(
      'href',
      'http://alertmanager/',
    );
  });

  it('renders unsafe URLs as plain text, never as links', () => {
    render(<AlertDetailOverview detail={detail({ generatorURL: 'javascript:alert(1)' })} />);

    expect(screen.queryByRole('link', { name: /javascript/ })).not.toBeInTheDocument();
    expect(screen.getByText('javascript:alert(1)')).toBeInTheDocument();
  });

  it('renders a placeholder for missing external URLs', () => {
    render(<AlertDetailOverview detail={detail({ generatorURL: '', externalURL: ' ' })} />);

    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getAllByText('—')).toHaveLength(2);
  });

  it('reports an unacknowledged alert without an actor note', () => {
    render(<AlertDetailOverview detail={detail()} />);

    expect(screen.getByText('Acknowledged').parentElement).toHaveTextContent('No');
  });

  it('shows the acknowledgement actor and timestamp when acknowledged', () => {
    render(
      <AlertDetailOverview
        detail={detail({
          acknowledged: true,
          acknowledgedBy: 'operator@example.com',
          acknowledgedAt: '2026-08-14T11:05:00Z',
        })}
      />,
    );

    const fact = screen.getByText('Acknowledged').parentElement;
    expect(fact).toHaveTextContent('acknowledged');
    expect(fact).toHaveTextContent('by operator@example.com');
    expect(fact).toHaveTextContent('at 2026-08-14 11:05:00 UTC');
  });

  it('omits the note when the API acknowledged without actor details', () => {
    render(<AlertDetailOverview detail={detail({ acknowledged: true })} />);

    const fact = screen.getByText('Acknowledged').parentElement;
    expect(fact).toHaveTextContent('acknowledged');
    expect(fact).not.toHaveTextContent('by ');
    expect(fact).not.toHaveTextContent('at ');
  });

  it('offers include/exclude filter buttons per label when a handler is present', () => {
    const onFilterLabel = vi.fn();
    render(<AlertDetailOverview detail={detail()} onFilterLabel={onFilterLabel} />);

    fireEvent.click(screen.getByRole('button', { name: 'Filter to team="core"' }));
    expect(onFilterLabel).toHaveBeenCalledWith({ name: 'team', op: '=', value: 'core' });

    fireEvent.click(screen.getByRole('button', { name: 'Exclude team="core"' }));
    expect(onFilterLabel).toHaveBeenCalledWith({ name: 'team', op: '!=', value: 'core' });

    fireEvent.click(screen.getByRole('button', { name: 'Filter to alertname="HighErrorRate"' }));
    expect(onFilterLabel).toHaveBeenCalledWith({
      name: 'alertname',
      op: '=',
      value: 'HighErrorRate',
    });
  });

  it('omits the label filter buttons without a handler', () => {
    render(<AlertDetailOverview detail={detail()} />);

    expect(screen.queryByRole('button', { name: /filter to /i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /exclude /i })).not.toBeInTheDocument();
    // The copy controls stay.
    expect(screen.getByRole('button', { name: 'Copy team' })).toBeInTheDocument();
  });

  it('never adds filter buttons to annotations', () => {
    render(<AlertDetailOverview detail={detail()} onFilterLabel={vi.fn()} />);

    expect(screen.queryByRole('button', { name: /filter to summary/i })).not.toBeInTheDocument();
  });

  it('renders the acknowledge action only with both permission and handler', () => {
    const onAcknowledge = vi.fn().mockResolvedValue(undefined);
    const { rerender } = render(
      <AlertDetailOverview
        detail={detail({ actions: { canAcknowledge: true } })}
        onAcknowledge={onAcknowledge}
      />,
    );
    expect(screen.getByRole('button', { name: 'Acknowledge alert' })).toBeInTheDocument();

    // Permission without a handler: no control.
    rerender(<AlertDetailOverview detail={detail({ actions: { canAcknowledge: true } })} />);
    expect(screen.queryByRole('button', { name: /acknowledge/i })).not.toBeInTheDocument();

    // Handler without the permission: no control.
    rerender(<AlertDetailOverview detail={detail()} onAcknowledge={onAcknowledge} />);
    expect(screen.queryByRole('button', { name: /acknowledge/i })).not.toBeInTheDocument();
  });

  it('toggles the acknowledgement through the handler', async () => {
    const onAcknowledge = vi.fn().mockResolvedValue(undefined);
    render(
      <AlertDetailOverview
        detail={detail({
          acknowledged: true,
          acknowledgedBy: 'operator@example.com',
          actions: { canAcknowledge: true },
        })}
        onAcknowledge={onAcknowledge}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Remove acknowledgement' }));

    await waitFor(() => expect(onAcknowledge).toHaveBeenCalledWith(false));
  });
});
