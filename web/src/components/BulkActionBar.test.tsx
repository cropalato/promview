import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { BulkResult } from '../alerts/bulk';
import { BulkActionBar } from './BulkActionBar';

function result(overrides: Partial<BulkResult> = {}): BulkResult {
  return { applied: 1, unchanged: 0, notFound: 0, results: [], partial: false, ...overrides };
}

function bar(props: Partial<React.ComponentProps<typeof BulkActionBar>> = {}) {
  return render(
    <BulkActionBar
      count={3}
      canOperate
      onAcknowledge={async () => result()}
      onClose={async () => result()}
      onAssign={async () => result()}
      onNote={async () => result()}
      onClear={() => {}}
      {...props}
    />,
  );
}

describe('BulkActionBar', () => {
  it('stays out of the way without a selection', () => {
    const { container } = bar({ count: 0 });
    expect(container.firstChild).toBeNull();
  });

  it('offers no actions to a viewer', () => {
    bar({ canOperate: false });
    expect(screen.queryByRole('button', { name: 'Acknowledge' })).toBeNull();
    expect(screen.getByText('Read-only access')).toBeTruthy();
  });

  it('asks for an assignee inline and applies it', async () => {
    const onAssign = vi.fn().mockResolvedValue(result());
    bar({ onAssign });
    fireEvent.click(screen.getByRole('button', { name: 'Assign…' }));
    fireEvent.change(screen.getByLabelText('Assign to (empty clears)'), {
      target: { value: 'platform-rota' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Apply to 3' }));
    await waitFor(() => expect(onAssign).toHaveBeenCalledWith('platform-rota'));
  });

  it('lets an empty assignment through, because that is how an owner is cleared', async () => {
    const onAssign = vi.fn().mockResolvedValue(result());
    bar({ onAssign });
    fireEvent.click(screen.getByRole('button', { name: 'Assign…' }));
    fireEvent.click(screen.getByRole('button', { name: 'Apply to 3' }));
    await waitFor(() => expect(onAssign).toHaveBeenCalledWith(''));
  });

  it('will not send an empty note, which the server would refuse anyway', () => {
    const onNote = vi.fn().mockResolvedValue(result());
    bar({ onNote });
    fireEvent.click(screen.getByRole('button', { name: 'Note…' }));
    expect((screen.getByRole('button', { name: 'Apply to 3' }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(onNote).not.toHaveBeenCalled();
  });

  it('writes a note in a textarea, as the single-alert composer does', () => {
    bar();
    fireEvent.click(screen.getByRole('button', { name: 'Note…' }));
    expect(screen.getByLabelText('Note for every selected alert').tagName).toBe('TEXTAREA');
  });

  it('reports all three outcomes rather than collapsing them', async () => {
    bar({ onClose: async () => result({ applied: 1, unchanged: 1, notFound: 1, partial: true }) });
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    await waitFor(() =>
      expect(screen.getByRole('status').textContent).toBe(
        '1 changed, 1 already done, 1 not visible to you',
      ),
    );
  });

  it('surfaces a failure instead of reporting a result', async () => {
    bar({ onClose: async () => Promise.reject(new Error('HTTP 403')) });
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toBe('HTTP 403'));
  });
});
