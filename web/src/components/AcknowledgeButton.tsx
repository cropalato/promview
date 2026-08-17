import { useState } from 'react';

interface AcknowledgeButtonProps {
  /** Current acknowledgement state; the button toggles it. */
  acknowledged: boolean;
  /** Runs the acknowledge API call; resolves when the server confirms. */
  onAcknowledge: (acknowledged: boolean) => Promise<void>;
}

/**
 * Operator control toggling an alert's acknowledgement. The host only renders
 * it when the server granted the per-alert acknowledge permission. While the
 * request runs the button is disabled and marked busy; failures surface
 * inline (role="alert") and leave the button enabled so the operator can
 * retry. The previous label stays visible while pending so the control never
 * collapses or shifts width between attempts.
 */
export function AcknowledgeButton({ acknowledged, onAcknowledge }: AcknowledgeButtonProps) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleClick = () => {
    setPending(true);
    setError(null);
    onAcknowledge(!acknowledged)
      .then(() => {
        setPending(false);
      })
      .catch((cause: unknown) => {
        setPending(false);
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  const label = acknowledged ? 'Remove acknowledgement' : 'Acknowledge alert';

  return (
    <div className="detail-action">
      <button
        type="button"
        className="button"
        onClick={handleClick}
        disabled={pending}
        aria-busy={pending}
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
