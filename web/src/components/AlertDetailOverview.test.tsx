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
});
