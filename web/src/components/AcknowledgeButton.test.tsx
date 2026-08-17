import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AcknowledgeButton } from './AcknowledgeButton';

describe('AcknowledgeButton', () => {
  it('offers to acknowledge an unacknowledged alert', async () => {
    const onAcknowledge = vi.fn().mockResolvedValue(undefined);
    render(<AcknowledgeButton acknowledged={false} onAcknowledge={onAcknowledge} />);

    const button = screen.getByRole('button', { name: 'Acknowledge alert' });
    expect(button).toBeEnabled();
    expect(button).toHaveAttribute('aria-busy', 'false');

    fireEvent.click(button);

    await waitFor(() => expect(onAcknowledge).toHaveBeenCalledWith(true));
    await waitFor(() => expect(button).toBeEnabled());
  });

  it('offers to remove an existing acknowledgement', async () => {
    const onAcknowledge = vi.fn().mockResolvedValue(undefined);
    render(<AcknowledgeButton acknowledged={true} onAcknowledge={onAcknowledge} />);

    fireEvent.click(screen.getByRole('button', { name: 'Remove acknowledgement' }));

    await waitFor(() => expect(onAcknowledge).toHaveBeenCalledWith(false));
  });

  it('disables the control and marks it busy while the request runs', async () => {
    let resolve: (() => void) | undefined;
    const onAcknowledge = vi.fn().mockImplementation(
      () =>
        new Promise<void>((done) => {
          resolve = done;
        }),
    );
    render(<AcknowledgeButton acknowledged={false} onAcknowledge={onAcknowledge} />);

    const button = screen.getByRole('button', { name: /acknowledge alert/i });
    fireEvent.click(button);

    await waitFor(() => expect(button).toBeDisabled());
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(onAcknowledge).toHaveBeenCalledTimes(1);

    // A second click while pending must not fire another request.
    fireEvent.click(button);
    expect(onAcknowledge).toHaveBeenCalledTimes(1);

    resolve?.();
    await waitFor(() => expect(button).toBeEnabled());
    expect(button).toHaveAttribute('aria-busy', 'false');
  });

  it('shows failures inline and stays retryable', async () => {
    const onAcknowledge = vi
      .fn()
      .mockRejectedValueOnce(new Error('Acknowledge request failed (HTTP 403)'))
      .mockResolvedValueOnce(undefined);
    render(<AcknowledgeButton acknowledged={false} onAcknowledge={onAcknowledge} />);

    const button = screen.getByRole('button', { name: 'Acknowledge alert' });
    fireEvent.click(button);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(/HTTP 403/);
    expect(button).toBeEnabled();

    fireEvent.click(button);

    await waitFor(() => expect(onAcknowledge).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  });
});
