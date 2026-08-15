import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { AlertHistoryEvent } from '../alerts/detail';
import { AlertTimeline } from './AlertTimeline';

function historyEvent(overrides: Partial<AlertHistoryEvent> = {}): AlertHistoryEvent {
  return {
    id: 1,
    occurrence: 1,
    type: 'created',
    typeLabel: 'Created',
    sourceStatus: 'firing',
    actor: 'alertmanager',
    message: '',
    occurredAt: '2026-08-14T10:00:00Z',
    ...overrides,
  };
}

describe('AlertTimeline', () => {
  it('renders events newest first, grouped by occurrence', () => {
    render(
      <AlertTimeline
        history={[
          historyEvent({ id: 1, occurrence: 1, typeLabel: 'Created' }),
          historyEvent({
            id: 2,
            occurrence: 1,
            type: 'resolved',
            typeLabel: 'Resolved',
            occurredAt: '2026-08-14T11:00:00Z',
          }),
          historyEvent({
            id: 3,
            occurrence: 2,
            type: 'reopened',
            typeLabel: 'Reopened',
            occurredAt: '2026-08-14T12:00:00Z',
          }),
        ]}
      />,
    );

    const groupHeadings = screen
      .getAllByRole('heading', { level: 3 })
      .map((heading) => heading.textContent);
    expect(groupHeadings).toEqual(['Occurrence 2', 'Occurrence 1']);

    const typeLabels = screen
      .getAllByText(/^(Created|Resolved|Reopened)$/)
      .map((element) => element.textContent);
    expect(typeLabels).toEqual(['Reopened', 'Resolved', 'Created']);
  });

  it('shows human labels for every lifecycle type and preserves unknown ones', () => {
    render(
      <AlertTimeline
        history={[
          historyEvent({ id: 5, type: 'imported', typeLabel: 'Imported', message: 'Bulk import' }),
          historyEvent({
            id: 6,
            type: 'silenced',
            typeLabel: 'silenced',
            occurredAt: '2026-08-14T13:00:00Z',
          }),
        ]}
      />,
    );

    expect(screen.getByText('Imported')).toBeInTheDocument();
    expect(screen.getByText('Bulk import')).toBeInTheDocument();
    expect(screen.getByText('silenced')).toBeInTheDocument();
  });

  it('renders actor and source status as metadata', () => {
    render(
      <AlertTimeline history={[historyEvent({ actor: 'am-eu', sourceStatus: 'resolved' })]} />,
    );

    expect(screen.getByText('am-eu · resolved')).toBeInTheDocument();
  });

  it('notes when the history is empty', () => {
    render(<AlertTimeline history={[]} />);

    expect(screen.getByText(/no history events recorded yet/i)).toBeInTheDocument();
  });
});
