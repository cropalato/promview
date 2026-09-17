import { useState } from 'react';
import type { BulkResult } from '../alerts/bulk';

interface BulkActionBarProps {
  count: number;
  /** Server-decided operator rights; the bar offers only what is permitted. */
  canOperate: boolean;
  onAcknowledge: () => Promise<BulkResult>;
  onClose: () => Promise<BulkResult>;
  onAssign: (assignee: string) => Promise<BulkResult>;
  onNote: (body: string) => Promise<BulkResult>;
  onClear: () => void;
}

/**
 * Appears only with a selection, and reports what actually happened to it.
 *
 * The three outcomes stay apart. `unchanged` is not folded into success — an
 * operator wants to tell "I changed forty" from "I changed two and the rest
 * were already done" — and `notFound` is worded as not visible rather than
 * refused, because the server deliberately does not distinguish an alert
 * outside a scope from one that does not exist, and saying "forbidden" would
 * leak exactly what that hides.
 */
export function BulkActionBar({
  count,
  canOperate,
  onAcknowledge,
  onClose,
  onAssign,
  onNote,
  onClear,
}: BulkActionBarProps) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<BulkResult | null>(null);
  // Which value the bar is currently asking for, if any. Inline rather than a
  // modal: the bar already owns the pending, error and result state, so a
  // failure lands where the outcome does, and a dialog would cover the list the
  // selection was just made from.
  const [composing, setComposing] = useState<'assign' | 'note' | null>(null);
  const [draft, setDraft] = useState('');

  if (count === 0) {
    return null;
  }

  const run = (action: () => Promise<BulkResult>) => {
    setPending(true);
    setError(null);
    setResult(null);
    action()
      .then((outcome) => {
        setPending(false);
        setResult(outcome);
      })
      .catch((cause: unknown) => {
        setPending(false);
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  const startComposing = (what: 'assign' | 'note') => {
    setComposing(what);
    setDraft('');
    setError(null);
    setResult(null);
  };

  const submitComposed = () => {
    const value = composing === 'note' ? draft.trim() : draft;
    // A note must say something; an assignment may be empty, because that is
    // how an owner is cleared.
    if (composing === 'note' && value === '') {
      return;
    }
    const action = composing === 'note' ? onNote : onAssign;
    setComposing(null);
    run(() => action(value));
  };

  return (
    <div className="bulk-bar" role="region" aria-label="Bulk actions">
      <span className="bulk-bar-count">{count} selected</span>
      {!canOperate ? (
        <span className="bulk-bar-note">Read-only access</span>
      ) : composing !== null ? (
        <form
          className="bulk-bar-form"
          onSubmit={(event) => {
            event.preventDefault();
            submitComposed();
          }}
        >
          <label className="bulk-bar-note" htmlFor="bulk-value">
            {composing === 'assign' ? 'Assign to (empty clears)' : 'Note for every selected alert'}
          </label>
          {composing === 'note' ? (
            // A textarea because a note is written in sentences, and the
            // single-alert composer is one too; a one-line field here would make
            // the bulk path worse than writing them individually.
            <textarea
              id="bulk-value"
              className="bulk-bar-input"
              rows={2}
              value={draft}
              autoFocus
              onChange={(event) => setDraft(event.target.value)}
            />
          ) : (
            <input
              id="bulk-value"
              className="bulk-bar-input"
              type="text"
              value={draft}
              autoFocus
              placeholder="nobody"
              onChange={(event) => setDraft(event.target.value)}
            />
          )}
          <button
            type="submit"
            className="button"
            disabled={pending || (composing === 'note' && draft.trim() === '')}
          >
            Apply to {count}
          </button>
          <button type="button" className="button" onClick={() => setComposing(null)}>
            Cancel
          </button>
        </form>
      ) : (
        <>
          <button
            type="button"
            className="button"
            disabled={pending}
            onClick={() => run(onAcknowledge)}
          >
            Acknowledge
          </button>
          <button type="button" className="button" disabled={pending} onClick={() => run(onClose)}>
            Close
          </button>
          <button
            type="button"
            className="button"
            disabled={pending}
            onClick={() => startComposing('assign')}
          >
            Assign…
          </button>
          <button
            type="button"
            className="button"
            disabled={pending}
            onClick={() => startComposing('note')}
          >
            Note…
          </button>
        </>
      )}
      <button type="button" className="button" disabled={pending} onClick={onClear}>
        Clear selection
      </button>
      {result !== null ? (
        <span className="bulk-bar-note" role="status">
          {describeResult(result)}
        </span>
      ) : null}
      {error !== null ? (
        <span className="bulk-bar-error" role="alert">
          {error}
        </span>
      ) : null}
    </div>
  );
}

/** Says what happened to all three groups, never collapsing them into one. */
function describeResult(result: BulkResult): string {
  const parts: string[] = [`${result.applied} changed`];
  if (result.unchanged > 0) {
    parts.push(`${result.unchanged} already done`);
  }
  if (result.notFound > 0) {
    parts.push(`${result.notFound} not visible to you`);
  }
  return parts.join(', ');
}
