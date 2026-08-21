import { useEffect, useId, useRef, useState } from 'react';
import { formatDuration, silenceDurationOptions } from '../alerts/silence';
import type { SilenceResponse } from '../alerts/silence';

interface SilenceDialogProps {
  /** What is about to be silenced, in the operator's words. */
  subject: string;
  /** The exact labels the silence will match on, shown before confirming. */
  matchers: Record<string, string>;
  /** How many alerts the silence covers, when the caller knows. */
  memberCount?: number;
  defaultSeconds: number;
  maxSeconds: number;
  onConfirm: (durationSeconds: number, comment: string) => Promise<SilenceResponse>;
  onClose: () => void;
}

/**
 * Confirmation for a silence.
 *
 * A silence hides alerts on a system promview does not own, so the dialog shows
 * the exact matchers before asking, not a summary of them: "silence this group"
 * is not something an operator can check, and `alertname="HighCPU"` is.
 *
 * Results are reported per Alertmanager rather than as one outcome. A group can
 * span several, and an operator told "done" while half the group still fires is
 * worse off than one told which half failed.
 */
export function SilenceDialog({
  subject,
  matchers,
  memberCount,
  defaultSeconds,
  maxSeconds,
  onConfirm,
  onClose,
}: SilenceDialogProps) {
  const [duration, setDuration] = useState(defaultSeconds);
  const [comment, setComment] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<SilenceResponse | null>(null);
  const titleId = useId();
  const durationId = useId();
  const commentId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);

  const options = silenceDurationOptions(defaultSeconds, maxSeconds);
  const entries = Object.entries(matchers).sort(([left], [right]) => left.localeCompare(right));

  useEffect(() => {
    dialogRef.current?.focus();
    const handleKey = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
      }
    };
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [onClose]);

  const handleConfirm = () => {
    setPending(true);
    setError(null);
    onConfirm(duration, comment.trim())
      .then((response) => {
        setPending(false);
        setResult(response);
      })
      .catch((cause: unknown) => {
        setPending(false);
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  const failures = result?.results.filter((entry) => entry.error !== undefined) ?? [];
  const successes = result?.results.filter((entry) => entry.error === undefined) ?? [];

  return (
    <div className="silence-backdrop" role="presentation" onClick={onClose}>
      <div
        className="silence-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        ref={dialogRef}
        onClick={(event) => event.stopPropagation()}
      >
        <h2 className="silence-title" id={titleId}>
          Silence {subject}
        </h2>

        {result === null ? (
          <>
            <p className="silence-copy">
              Alertmanager will stop notifying for
              {memberCount !== undefined
                ? ` ${memberCount} alert${memberCount === 1 ? '' : 's'}`
                : ''}{' '}
              matching:
            </p>
            {/* The matchers, not a description of them: this is the part an
                operator can actually check before hiding something. */}
            <dl className="silence-matchers">
              {entries.map(([name, value]) => (
                <div className="silence-matcher" key={name}>
                  <dt>{name}</dt>
                  <dd>{value}</dd>
                </div>
              ))}
            </dl>

            <label className="silence-field" htmlFor={durationId}>
              <span>Duration</span>
              <select
                id={durationId}
                value={duration}
                disabled={pending}
                onChange={(event) => setDuration(Number(event.target.value))}
              >
                {options.map((option) => (
                  <option key={option.seconds} value={option.seconds}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>

            <label className="silence-field" htmlFor={commentId}>
              <span>Comment</span>
              <input
                id={commentId}
                type="text"
                value={comment}
                disabled={pending}
                placeholder="why this is being silenced"
                onChange={(event) => setComment(event.target.value)}
              />
            </label>

            <p className="silence-note">
              Ends in {formatDuration(duration)}. Recorded against your account.
            </p>

            {error !== null ? (
              <p className="silence-error" role="alert">
                {error}
              </p>
            ) : null}

            <div className="silence-actions">
              <button type="button" className="button" onClick={onClose} disabled={pending}>
                Cancel
              </button>
              <button
                type="button"
                className="button button-primary"
                onClick={handleConfirm}
                disabled={pending}
                aria-busy={pending}
              >
                {pending ? 'Silencing…' : 'Silence'}
              </button>
            </div>
          </>
        ) : (
          <>
            {/* Per Alertmanager, so a partial application cannot read as done. */}
            <ul className="silence-results">
              {result.results.map((entry) => (
                <li
                  key={entry.source}
                  className={
                    entry.error === undefined ? 'silence-result-ok' : 'silence-result-failed'
                  }
                >
                  <span className="silence-result-source">{entry.source}</span>
                  <span>
                    {entry.error === undefined ? `silenced · ${entry.silenceId}` : entry.error}
                  </span>
                </li>
              ))}
            </ul>
            <p className="silence-note" role="status">
              {failures.length === 0
                ? `Silenced until ${result.endsAt} by ${result.createdBy}.`
                : `${successes.length} of ${result.results.length} Alertmanagers accepted the silence. The rest are still notifying.`}
            </p>
            <div className="silence-actions">
              <button type="button" className="button button-primary" onClick={onClose}>
                Close
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
