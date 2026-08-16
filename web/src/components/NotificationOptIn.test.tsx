import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { NotificationOptIn } from './NotificationOptIn';

describe('NotificationOptIn', () => {
  it('offers the opt-in while disabled and forwards clicks', () => {
    const onToggle = vi.fn();
    render(<NotificationOptIn state="disabled" onToggle={onToggle} />);

    const button = screen.getByRole('button', { name: /enable critical alert notifications/i });
    expect(button).toHaveAttribute('aria-pressed', 'false');
    expect(button).toBeEnabled();

    fireEvent.click(button);
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it('shows the enabled state as pressed and offers to mute', () => {
    render(<NotificationOptIn state="enabled" onToggle={() => {}} />);

    const button = screen.getByRole('button', { name: /mute critical alert notifications/i });
    expect(button).toHaveAttribute('aria-pressed', 'true');
    expect(button).toBeEnabled();
  });

  it('is inert when the browser denied permission', () => {
    const onToggle = vi.fn();
    render(<NotificationOptIn state="denied" onToggle={onToggle} />);

    const button = screen.getByRole('button', { name: /blocked in the browser settings/i });
    expect(button).toBeDisabled();

    fireEvent.click(button);
    expect(onToggle).not.toHaveBeenCalled();
  });

  it('is inert when notifications are unsupported', () => {
    const onToggle = vi.fn();
    render(<NotificationOptIn state="unsupported" onToggle={onToggle} />);

    const button = screen.getByRole('button', { name: /does not support notifications/i });
    expect(button).toBeDisabled();

    fireEvent.click(button);
    expect(onToggle).not.toHaveBeenCalled();
  });
});
