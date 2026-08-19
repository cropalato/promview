import { useRef } from 'react';
import type { KeyboardEvent, PointerEvent } from 'react';
import { MAX_COLUMN_WIDTH, MIN_COLUMN_WIDTH, clampColumnWidth } from '../preferences/columnWidths';

const KEYBOARD_STEP = 16;

interface ColumnResizeHandleProps {
  columnId: string;
  /** Column heading, used for the handle's accessible name. */
  columnLabel: string;
  /** The stored width; absent means the column keeps its default sizing. */
  width?: number;
  onResize: (columnId: string, width: number) => void;
  /** Drops the stored width so the column returns to its default sizing. */
  onReset: (columnId: string) => void;
}

/**
 * The grip on a column header's trailing edge.
 *
 * It is a `separator` per the APG window-splitter pattern: pointer and touch
 * drag it, ArrowLeft/ArrowRight step it (Shift for a larger step), and Home —
 * or a double-click — hands the column back to its default sizing. The visible
 * line is thinner than the hit area so touch stays usable; during a drag the
 * pointer is captured so leaving the header does not lose the gesture.
 */
export function ColumnResizeHandle({
  columnId,
  columnLabel,
  width,
  onResize,
  onReset,
}: ColumnResizeHandleProps) {
  const drag = useRef<{ pointerId: number; startX: number; startWidth: number } | null>(null);

  // A column never resized yet has no stored width; measure the header so the
  // first drag or keypress grows from what the operator actually sees.
  const currentWidth = (handle: HTMLSpanElement): number =>
    width ?? handle.parentElement?.getBoundingClientRect().width ?? MIN_COLUMN_WIDTH;

  const handlePointerDown = (event: PointerEvent<HTMLSpanElement>) => {
    if (event.button !== 0) {
      return;
    }
    // Keeping the gesture off the header matters: a sortable header's button
    // fills the cell, and a drag must not end up activating it.
    event.preventDefault();
    event.stopPropagation();
    drag.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startWidth: currentWidth(event.currentTarget),
    };
    event.currentTarget.setPointerCapture(event.pointerId);
  };

  const handlePointerMove = (event: PointerEvent<HTMLSpanElement>) => {
    if (drag.current === null || drag.current.pointerId !== event.pointerId) {
      return;
    }
    onResize(
      columnId,
      clampColumnWidth(drag.current.startWidth + event.clientX - drag.current.startX),
    );
  };

  const endDrag = (event: PointerEvent<HTMLSpanElement>) => {
    if (drag.current === null || drag.current.pointerId !== event.pointerId) {
      return;
    }
    drag.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLSpanElement>) => {
    const step = event.shiftKey ? KEYBOARD_STEP * 4 : KEYBOARD_STEP;
    if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
      event.preventDefault();
      const delta = event.key === 'ArrowRight' ? step : -step;
      onResize(columnId, clampColumnWidth(currentWidth(event.currentTarget) + delta));
    } else if (event.key === 'Home') {
      event.preventDefault();
      onReset(columnId);
    }
  };

  return (
    <span
      role="separator"
      aria-orientation="vertical"
      aria-label={`Resize ${columnLabel} column`}
      aria-valuenow={width}
      aria-valuemin={MIN_COLUMN_WIDTH}
      aria-valuemax={MAX_COLUMN_WIDTH}
      tabIndex={0}
      className="col-resize-handle"
      title={`Drag to resize ${columnLabel}; double-click to reset`}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onKeyDown={handleKeyDown}
      onDoubleClick={() => onReset(columnId)}
    />
  );
}
