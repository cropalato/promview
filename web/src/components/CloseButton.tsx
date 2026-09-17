import { useState } from 'react';

interface CloseButtonProps {
  closed: boolean;
  onClose: (closed: boolean) => Promise<void>;
}

/**
 * Files an alert as handled, or reopens it.
 *
 * Deliberately worded as filing rather than resolving: closing is
 * promview-local, writes nothing to Alertmanager, and leaves the alert firing
 * as far as the source is concerned. An operator who reads this as "make it
 * stop" would be surprised by the next notification.
 */
export function CloseButton({ closed, onClose }: CloseButtonProps) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleClick = () => {
    setPending(true);
    setError(null);
    onClose(!closed)
      .then(() => {
        setPending(false);
      })
      .catch((cause: unknown) => {
        setPending(false);
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  const label = closed ? 'Reopen alert' : 'Close alert';

  return (
    <div className="detail-action">
      <button
        type="button"
        className="button"
        onClick={handleClick}
        disabled={pending}
        aria-busy={pending}
        title={
          closed
            ? 'Put the alert back in the open list'
            : 'File as handled. Alertmanager is not told, and notifications continue.'
        }
      >
        {pending ? `${label}…` : label}
      </button>
      {error !== null ? (
        <p className="detail-action-error" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}
