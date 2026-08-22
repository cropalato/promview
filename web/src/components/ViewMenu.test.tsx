import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { defaultPreferences } from '../preferences/store';
import type { ColumnPreference, Preferences } from '../preferences/store';
import { ViewMenu } from './ViewMenu';

function withColumns(columns: ColumnPreference[]): Preferences {
  return { ...defaultPreferences(), columns };
}

/** The move buttons' accessible names, which are also the column order. */
function moveUpNames(): (string | null)[] {
  return screen
    .getAllByRole('button', { name: /^Move .+ up$/ })
    .map((button) => button.getAttribute('aria-label'));
}

describe('ViewMenu', () => {
  it('opens and toggles grouping', () => {
    const onChange = vi.fn();
    render(<ViewMenu preferences={defaultPreferences()} onChange={onChange} />);

    fireEvent.click(screen.getByRole('button', { name: 'View' }));
    fireEvent.click(screen.getByRole('checkbox', { name: /group alerts/i }));

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ grouping: expect.objectContaining({ enabled: false }) }),
    );
  });

  it("edits the notification selector in the filter bar's own syntax", () => {
    const onChange = vi.fn();
    render(<ViewMenu preferences={defaultPreferences()} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'View' }));

    const input = screen.getByRole('textbox', { name: /notification selector/i });
    expect(input).toHaveValue('severity="critical"');

    fireEvent.change(input, { target: { value: '{severity="critical", team="platform"}' } });
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        notifications: expect.objectContaining({
          matchers: [
            { name: 'severity', op: '=', value: 'critical' },
            { name: 'team', op: '=', value: 'platform' },
          ],
        }),
      }),
    );
  });

  it('refuses a selector on a field a stream event does not carry', () => {
    // The server rejects it too, but saying so here means the operator learns
    // which field is wrong while still looking at the box.
    const onChange = vi.fn();
    render(<ViewMenu preferences={defaultPreferences()} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'View' }));

    fireEvent.change(screen.getByRole('textbox', { name: /notification selector/i }), {
      target: { value: 'instance="web-01"' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));

    expect(screen.getByRole('alert')).toHaveTextContent(/cannot match on instance/i);
    expect(onChange).not.toHaveBeenCalled();
  });

  it('reports a selector that does not parse instead of saving it', () => {
    const onChange = vi.fn();
    render(<ViewMenu preferences={defaultPreferences()} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'View' }));

    fireEvent.change(screen.getByRole('textbox', { name: /notification selector/i }), {
      target: { value: 'severity=' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));

    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('shows what auto density currently resolves to', () => {
    // Choosing auto should not leave the operator guessing which row height
    // they are about to get.
    render(
      <ViewMenu preferences={defaultPreferences()} onChange={vi.fn()} resolvedDensity="compact" />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'View' }));
    expect(screen.getByRole('radio', { name: /auto/i })).toBeChecked();
    expect(screen.getByText(/now compact/i)).toBeInTheDocument();
  });

  it('adds a label column', () => {
    const onChange = vi.fn();
    render(<ViewMenu preferences={defaultPreferences()} onChange={onChange} />);

    fireEvent.click(screen.getByRole('button', { name: 'View' }));
    fireEvent.change(screen.getByLabelText('Label name'), {
      target: { value: 'prometheus_cluster' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    const next = onChange.mock.calls[0]?.[0];
    expect(next.columns.at(-1)).toEqual({ id: 'label:prometheus_cluster' });
  });

  it('lists kept fixed and label columns in the saved order', () => {
    render(
      <ViewMenu
        preferences={withColumns([{ id: 'alert' }, { id: 'label:cluster' }, { id: 'severity' }])}
        onChange={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'View' }));

    // Registry order would be severity first; the saved order is what shows.
    expect(moveUpNames()).toEqual(['Move Alert up', 'Move cluster up', 'Move Severity up']);
    // A label column names itself, and moving it is reachable by name.
    expect(screen.getByRole('button', { name: 'Move cluster down' })).toBeEnabled();
  });

  it('disables the move buttons at the ends of the order', () => {
    render(
      <ViewMenu
        preferences={withColumns([{ id: 'alert' }, { id: 'label:cluster' }, { id: 'severity' }])}
        onChange={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'View' }));

    expect(screen.getByRole('button', { name: 'Move Alert up' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Move Alert down' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Move Severity up' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Move Severity down' })).toBeDisabled();
  });

  it('moves the stored column whole and keeps ids it cannot show', () => {
    const onChange = vi.fn();
    render(
      <ViewMenu
        preferences={withColumns([
          { id: 'alert' },
          { id: 'label:cluster' },
          // Saved by a console this one does not know; it has no row, and a
          // reorder must not be what drops it.
          { id: 'mystery' },
          { id: 'severity', width: 140 },
        ])}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'View' }));
    fireEvent.click(screen.getByRole('button', { name: 'Move Severity up' }));

    // The width rides along: the move carries the preference object, not a
    // rebuilt { id }.
    expect(onChange.mock.calls[0]?.[0].columns).toEqual([
      { id: 'alert' },
      { id: 'severity', width: 140 },
      { id: 'mystery' },
      { id: 'label:cluster' },
    ]);
  });

  it('turns a fixed column back on from below the order', () => {
    const onChange = vi.fn();
    render(<ViewMenu preferences={withColumns([{ id: 'alert' }])} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'View' }));

    expect(moveUpNames()).toEqual(['Move Alert up']);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Team' }));

    expect(onChange.mock.calls[0]?.[0].columns).toEqual([{ id: 'alert' }, { id: 'team' }]);
  });
});
