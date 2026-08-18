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
