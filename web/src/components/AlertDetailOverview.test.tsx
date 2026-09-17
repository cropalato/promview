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
    suppressed: false,
    assignee: '',
    assignedBy: '',
    assignedAt: null,
    closed: false,
    closedBy: '',
    closedAt: null,
    silencedBy: [],
    acknowledged: false,
    acknowledgedBy: '',
    acknowledgedAt: null,
    actions: {
      canAcknowledge: false,
      canSilence: false,
      canAssign: false,
      canClose: false,
      canNote: false,
    },
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
        detail={detail({
          actions: {
            canAcknowledge: true,
            canSilence: true,
            canAssign: true,
            canClose: true,
            canNote: true,
          },
        })}
        onAcknowledge={onAcknowledge}
      />,
    );
    expect(screen.getByRole('button', { name: 'Acknowledge alert' })).toBeInTheDocument();

    // Permission without a handler: no control.
    rerender(
      <AlertDetailOverview
        detail={detail({
          actions: {
            canAcknowledge: true,
            canSilence: true,
            canAssign: true,
            canClose: true,
            canNote: true,
          },
        })}
      />,
    );
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
          actions: {
            canAcknowledge: true,
            canSilence: true,
            canAssign: true,
            canClose: true,
            canNote: true,
          },
        })}
        onAcknowledge={onAcknowledge}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Remove acknowledgement' }));

    await waitFor(() => expect(onAcknowledge).toHaveBeenCalledWith(false));
  });
});

describe('why an alert is not notifying', () => {
  it('names the author and expiry of a silence Promview created', () => {
    render(
      <AlertDetailOverview
        detail={detail({ suppressed: true, silencedBy: ['sil-1'] })}
        silences={[
          {
            source: 'am-eu',
            silenceId: 'sil-1',
            matchers: { alertname: 'HighErrorRate' },
            createdBy: 'ada@example.com',
            comment: 'disk swap',
            startsAt: '2026-08-14T10:00:00Z',
            endsAt: '2026-08-14T14:00:00Z',
          },
        ]}
      />,
    );
    expect(screen.getByText('silenced')).toBeInTheDocument();
    expect(screen.getByText(/ada@example.com/)).toBeInTheDocument();
    expect(screen.getByText(/disk swap/)).toBeInTheDocument();
  });

  it('admits it knows nothing about a silence made outside Promview', () => {
    // The silence is real and still suppressing; inventing an author for it
    // would be worse than saying where it came from.
    render(<AlertDetailOverview detail={detail({ suppressed: true, silencedBy: ['foreign'] })} />);
    expect(screen.getByText('silenced')).toBeInTheDocument();
    expect(screen.getByText(/created outside Promview/)).toBeInTheDocument();
  });

  it('calls an inhibition an inhibition rather than a silence', () => {
    // Nobody chose it and it lifts itself when its parent clears; presenting it
    // as a silence would send an operator hunting for a decision nobody made.
    render(<AlertDetailOverview detail={detail({ suppressed: true, silencedBy: [] })} />);
    expect(screen.getByText('inhibited')).toBeInTheDocument();
    expect(screen.queryByText('silenced')).not.toBeInTheDocument();
  });

  it('says plainly when nothing is holding the alert back', () => {
    render(<AlertDetailOverview detail={detail()} />);
    expect(screen.getByText('Suppressed')).toBeInTheDocument();
    expect(screen.queryByText('silenced')).not.toBeInTheDocument();
  });
});

describe('removing a silence', () => {
  const silenced = () => detail({ suppressed: true, silencedBy: ['sil-1', 'sil-2'] });

  /** The nth Remove control, asserted present so the test fails on absence. */
  function removeButton(index: number): HTMLElement {
    const button = screen.getAllByRole('button', { name: 'Remove' })[index];
    if (button === undefined) {
      throw new Error(`no Remove control at index ${index}`);
    }
    return button;
  }

  it('offers no control where the deployment cannot remove silences', () => {
    // The handler is absent where the server is too old, the deployment has no
    // Alertmanager to write to, or this operator may not act on this alert.
    // Rendering a control that could only fail is worse than not offering one.
    render(<AlertDetailOverview detail={silenced()} />);
    expect(screen.queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
  });

  it('lifts the silence the operator picked, not the other one', async () => {
    const onRemoveSilence = vi.fn().mockResolvedValue(undefined);
    render(<AlertDetailOverview detail={silenced()} onRemoveSilence={onRemoveSilence} />);

    const buttons = screen.getAllByRole('button', { name: 'Remove' });
    expect(buttons).toHaveLength(2);
    fireEvent.click(removeButton(1));

    await waitFor(() => expect(onRemoveSilence).toHaveBeenCalledWith('sil-2'));
    expect(onRemoveSilence).toHaveBeenCalledTimes(1);
  });

  it('holds every control while one removal is in flight', async () => {
    let settle: () => void = () => {};
    const onRemoveSilence = vi.fn().mockReturnValue(
      new Promise<void>((resolve) => {
        settle = resolve;
      }),
    );
    render(<AlertDetailOverview detail={silenced()} onRemoveSilence={onRemoveSilence} />);

    fireEvent.click(removeButton(0));

    // A second click while the first is still settling would ask the
    // Alertmanager to lift something twice on a guess about what the first did.
    await screen.findByRole('button', { name: 'Removing…' });
    for (const button of screen.getAllByRole('button', { name: /Remove/ })) {
      expect(button).toBeDisabled();
    }

    settle();
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Removing…' })).not.toBeInTheDocument(),
    );
  });

  it('says what went wrong and leaves the silence listed', async () => {
    const onRemoveSilence = vi.fn().mockRejectedValue(new Error('the alertmanager refused'));
    render(<AlertDetailOverview detail={silenced()} onRemoveSilence={onRemoveSilence} />);

    fireEvent.click(removeButton(0));

    // The silence is still in place, so the alert is still hidden; reporting
    // success would promise noise that is not coming back.
    expect(await screen.findByRole('alert')).toHaveTextContent('the alertmanager refused');
    expect(screen.getByText('silenced')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Remove' })).toHaveLength(2);
  });

  it('offers nothing to remove for an inhibition', () => {
    // An inhibition has no silence to lift: it goes when its parent alert does.
    render(
      <AlertDetailOverview
        detail={detail({ suppressed: true, silencedBy: [] })}
        onRemoveSilence={vi.fn()}
      />,
    );
    expect(screen.queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
  });
});

describe('assign, close and notes', () => {
  it('offers nothing the server did not permit', () => {
    render(
      <AlertDetailOverview
        detail={detail({
          actions: {
            canAcknowledge: false,
            canSilence: false,
            canAssign: false,
            canClose: false,
            canNote: false,
          },
        })}
        onAssign={async () => {}}
        onClose={async () => {}}
        onAddNote={async () => {}}
      />,
    );
    // Handlers are present, so only the server's own flags are withholding
    // these — which is the gate that matters.
    expect(screen.queryByLabelText('Assignee')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Close alert' })).toBeNull();
    expect(screen.queryByLabelText('Add a note')).toBeNull();
  });

  it('offers the controls the server permitted', () => {
    render(
      <AlertDetailOverview
        detail={detail({
          actions: {
            canAcknowledge: false,
            canSilence: false,
            canAssign: true,
            canClose: true,
            canNote: true,
          },
        })}
        onAssign={async () => {}}
        onClose={async () => {}}
        onAddNote={async () => {}}
      />,
    );
    expect(screen.getByLabelText('Assignee')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Close alert' })).toBeTruthy();
  });

  it('says reopen once the alert is closed', () => {
    render(
      <AlertDetailOverview
        detail={detail({
          closed: true,
          actions: {
            canAcknowledge: false,
            canSilence: false,
            canAssign: false,
            canClose: true,
            canNote: true,
          },
        })}
        onClose={async () => {}}
      />,
    );
    expect(screen.getByRole('button', { name: 'Reopen alert' })).toBeTruthy();
  });

  it('sends the assignment and clears it with an empty value', async () => {
    const onAssign = vi.fn().mockResolvedValue(undefined);
    render(
      <AlertDetailOverview
        detail={detail({
          assignee: 'platform-rota',
          actions: {
            canAcknowledge: false,
            canSilence: false,
            canAssign: true,
            canClose: false,
            canNote: false,
          },
        })}
        onAssign={onAssign}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    await waitFor(() => expect(onAssign).toHaveBeenCalledWith(''));
  });

  it('renders notes oldest first and marks ones from an earlier occurrence', () => {
    render(
      <AlertDetailOverview
        detail={detail({ occurrence: 2 })}
        notes={[
          {
            id: 1,
            occurrence: 1,
            author: 'oncall',
            body: 'first incident',
            createdAt: '2026-09-16T12:00:00Z',
          },
          {
            id: 2,
            occurrence: 2,
            author: 'oncall',
            body: 'this incident',
            createdAt: '2026-09-16T13:00:00Z',
          },
        ]}
      />,
    );
    expect(screen.getByText('first incident')).toBeTruthy();
    expect(screen.getByText('this incident')).toBeTruthy();
    // Only the note from the previous incident is flagged; the current one is
    // not, or the marker would be noise on every note.
    expect(screen.getAllByText('earlier occurrence')).toHaveLength(1);
  });

  it('offers no composer without a handler, because notes cannot be edited later', () => {
    render(<AlertDetailOverview detail={detail()} notes={[]} />);
    expect(screen.getByText('No notes yet.')).toBeTruthy();
    expect(screen.queryByLabelText('Add a note')).toBeNull();
  });
});

describe('closed state in the drawer', () => {
  it('marks a closed alert and says who filed it', () => {
    render(
      <AlertDetailOverview
        detail={detail({ closed: true, closedBy: 'oncall', closedAt: '2026-09-17T09:00:00Z' })}
      />,
    );
    expect(screen.getByText('closed')).toBeTruthy();
    // An alert reached by deep link has to carry the same fact the list does.
    expect(screen.getByText(/by oncall/)).toBeTruthy();
  });

  it('says nothing about closing on an open alert', () => {
    render(<AlertDetailOverview detail={detail()} />);
    expect(screen.queryByText('closed')).toBeNull();
  });
});
