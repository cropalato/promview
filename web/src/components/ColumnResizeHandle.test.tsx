import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { MAX_COLUMN_WIDTH, MIN_COLUMN_WIDTH } from '../preferences/columnWidths';
import { ColumnResizeHandle } from './ColumnResizeHandle';

function renderHandle(width: number | undefined = 200) {
  const onResize = vi.fn();
  const onReset = vi.fn();
  render(
    <table>
      <thead>
        <tr>
          <th>
            Summary
            <ColumnResizeHandle
              columnId="summary"
              columnLabel="Summary"
              width={width}
              onResize={onResize}
              onReset={onReset}
            />
          </th>
        </tr>
      </thead>
    </table>,
  );
  // Last in case a test renders more than one handle.
  const handle = screen.getAllByRole('separator').at(-1);
  if (handle === undefined) {
    throw new Error('expected a resize handle to be rendered');
  }
  return { onResize, onReset, handle };
}

describe('ColumnResizeHandle', () => {
  it('exposes a labelled, focusable separator with range metadata', () => {
    const { handle } = renderHandle(200);
    expect(handle).toHaveAccessibleName('Resize Summary column');
    expect(handle).toHaveAttribute('aria-orientation', 'vertical');
    expect(handle).toHaveAttribute('aria-valuenow', '200');
    expect(handle).toHaveAttribute('aria-valuemin', String(MIN_COLUMN_WIDTH));
    expect(handle).toHaveAttribute('aria-valuemax', String(MAX_COLUMN_WIDTH));
    expect(handle).toHaveAttribute('tabindex', '0');
  });

  it('steps with the arrow keys, clamped to the supported range', () => {
    const { handle, onResize } = renderHandle(200);
    fireEvent.keyDown(handle, { key: 'ArrowRight' });
    expect(onResize).toHaveBeenLastCalledWith('summary', 216);
    fireEvent.keyDown(handle, { key: 'ArrowLeft', shiftKey: true });
    expect(onResize).toHaveBeenLastCalledWith('summary', 136);

    const narrow = renderHandle(MIN_COLUMN_WIDTH);
    fireEvent.keyDown(narrow.handle, { key: 'ArrowLeft' });
    expect(narrow.onResize).toHaveBeenLastCalledWith('summary', MIN_COLUMN_WIDTH);
  });

  it('resets to content sizing on Home and on double-click', () => {
    const { handle, onReset } = renderHandle(200);
    fireEvent.keyDown(handle, { key: 'Home' });
    fireEvent.doubleClick(handle);
    expect(onReset).toHaveBeenCalledTimes(2);
    expect(onReset).toHaveBeenCalledWith('summary');
  });

  it('drags from the current width and stops on pointer up', () => {
    const { handle, onResize } = renderHandle(200);
    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientX: 100 });
    fireEvent.pointerMove(handle, { pointerId: 1, clientX: 140 });
    expect(onResize).toHaveBeenLastCalledWith('summary', 240);
    fireEvent.pointerUp(handle, { pointerId: 1, clientX: 140 });
    fireEvent.pointerMove(handle, { pointerId: 1, clientX: 300 });
    expect(onResize).toHaveBeenCalledTimes(1);
  });

  it('ignores a drag that is not the primary button', () => {
    const { handle, onResize } = renderHandle(200);
    fireEvent.pointerDown(handle, { button: 2, pointerId: 1, clientX: 100 });
    fireEvent.pointerMove(handle, { pointerId: 1, clientX: 200 });
    expect(onResize).not.toHaveBeenCalled();
  });

  it('clamps a drag that runs past the maximum', () => {
    const { handle, onResize } = renderHandle(200);
    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientX: 0 });
    fireEvent.pointerMove(handle, { pointerId: 1, clientX: 5000 });
    expect(onResize).toHaveBeenLastCalledWith('summary', MAX_COLUMN_WIDTH);
  });
});
