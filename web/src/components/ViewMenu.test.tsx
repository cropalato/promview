import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { defaultPreferences } from '../preferences/store';
import { ViewMenu } from './ViewMenu';

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
});
