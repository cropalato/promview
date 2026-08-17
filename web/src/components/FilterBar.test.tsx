import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { FilterBar } from './FilterBar';

function renderBar(overrides: Partial<Parameters<typeof FilterBar>[0]> = {}) {
  const props = {
    value: '',
    shown: 0,
    total: 0,
    onChange: vi.fn(),
    onApply: vi.fn(),
    ...overrides,
  };
  render(<FilterBar {...props} />);
  return props;
}

describe('FilterBar', () => {
  it('applies the draft on Enter and on form submit', () => {
    const props = renderBar({ value: 'severity="critical"' });
    const input = screen.getByRole('textbox', { name: /filter alerts/i });

    fireEvent.keyDown(input, { key: 'Enter' });
    expect(props.onApply).toHaveBeenCalledWith('severity="critical"');

    fireEvent.submit(screen.getByRole('search', { name: /alert filter/i }));
    expect(props.onApply).toHaveBeenCalledTimes(2);
  });

  it('clears the draft and the applied filter on Escape', () => {
    const props = renderBar({ value: 'severity="critical"' });
    const input = screen.getByRole('textbox', { name: /filter alerts/i });

    fireEvent.keyDown(input, { key: 'Escape' });

    expect(props.onChange).toHaveBeenCalledWith('');
    expect(props.onApply).toHaveBeenCalledWith('');
  });

  it('shows the parse error and marks the input invalid', () => {
    renderBar({
      value: 'critical',
      error: 'expected one of = or != after the label name (column 9)',
    });

    const input = screen.getByRole('textbox', { name: /filter alerts/i });
    expect(input).toHaveAttribute('aria-invalid', 'true');
    const message = screen.getByRole('alert');
    expect(message).toHaveTextContent(/expected one of = or !=/);
    expect(input).toHaveAttribute('aria-describedby', message.id);
  });

  it('renders no error region when the draft is valid', () => {
    renderBar({ value: 'severity="critical"', error: null });

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: /filter alerts/i })).not.toHaveAttribute(
      'aria-invalid',
    );
  });

  it('reports shown against total alerts', () => {
    renderBar({ shown: 2, total: 5 });

    expect(screen.getByText('2 of 5 alerts')).toBeInTheDocument();
  });
});
