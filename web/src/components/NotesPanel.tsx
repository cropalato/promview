import { useState } from 'react';
import type { AlertNote } from '../alerts/detail';
import { formatTimestamp } from '../alerts/format';

interface NotesPanelProps {
  notes: readonly AlertNote[];
  /** The alert's current occurrence, so notes about a previous one are marked. */
  occurrence: number;
  /** Absent where this operator may not write; the composer is then not rendered. */
  onAddNote?: (body: string) => Promise<void>;
}

/**
 * What operators wrote about this alert, oldest first — the order a handover is
 * read in.
 *
 * There is no edit and no delete because the API offers neither, deliberately:
 * a note is what somebody relied on at the time, and one that can be quietly
 * rewritten afterwards is worth less than none. Offering a control that cannot
 * work would be worse than the absence.
 */
export function NotesPanel({ notes, occurrence, onAddNote }: NotesPanelProps) {
  const [draft, setDraft] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = () => {
    const body = draft.trim();
    if (body === '' || onAddNote === undefined) {
      return;
    }
    setPending(true);
    setError(null);
    onAddNote(body)
      .then(() => {
        setPending(false);
        setDraft('');
      })
      .catch((cause: unknown) => {
        setPending(false);
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  return (
    <section className="detail-section" aria-label="Notes">
      <h3 className="detail-section-title">Notes</h3>
      {notes.length === 0 ? (
        <p className="detail-empty-note">No notes yet.</p>
      ) : (
        <ol className="detail-notes">
          {notes.map((note) => (
            <li key={note.id} className="detail-note">
              <div className="detail-note-meta detail-mono">
                <span>{note.author || 'unknown'}</span>
                <span>{formatTimestamp(note.createdAt)}</span>
                {/* An alert that resolved and fired again is a different
                    incident. Saying so keeps an old note from reading as
                    though it describes this one. */}
                {note.occurrence !== occurrence ? (
                  <span
                    className="detail-note-occurrence"
                    title={`Written during occurrence ${note.occurrence}; this is ${occurrence}`}
                  >
                    earlier occurrence
                  </span>
                ) : null}
              </div>
              <p className="detail-note-body">{note.body}</p>
            </li>
          ))}
        </ol>
      )}
      {onAddNote !== undefined ? (
        <form
          className="detail-note-form"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <label className="detail-assignee-label" htmlFor="alert-note">
            Add a note
          </label>
          <textarea
            id="alert-note"
            className="detail-note-input"
            rows={3}
            value={draft}
            placeholder="What was checked, what was ruled out, who was called"
            disabled={pending}
            onChange={(event) => setDraft(event.target.value)}
          />
          <button
            type="submit"
            className="button"
            disabled={pending || draft.trim() === ''}
            aria-busy={pending}
          >
            {pending ? 'Adding…' : 'Add note'}
          </button>
          {error !== null ? (
            <p className="detail-action-error" role="alert">
              {error}
            </p>
          ) : null}
        </form>
      ) : null}
    </section>
  );
}
