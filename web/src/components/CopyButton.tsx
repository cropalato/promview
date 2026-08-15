import { useEffect, useRef, useState } from 'react';

interface CopyButtonProps {
  /** Text written to the clipboard. */
  value: string;
  /** Accessible name, e.g. "Copy team". */
  label: string;
}

/** Revert delay for the "Copied" confirmation. */
export const COPY_FEEDBACK_MS = 1500;

/**
 * Quiet copy-to-clipboard control used by the detail drawer. Shows a brief
 * confirmation in the button label; when the clipboard API is unavailable or
 * denied the button simply stays unchanged.
 */
export function CopyButton({ value, label }: CopyButtonProps) {
  const [copied, setCopied] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
      }
    },
    [],
  );

  const handleCopy = () => {
    const clipboard = navigator.clipboard;
    if (clipboard === undefined) {
      return;
    }
    clipboard
      .writeText(value)
      .then(() => {
        setCopied(true);
        if (timerRef.current !== null) {
          clearTimeout(timerRef.current);
        }
        timerRef.current = setTimeout(() => setCopied(false), COPY_FEEDBACK_MS);
      })
      .catch(() => {
        // Clipboard denied: leave the button unchanged.
      });
  };

  return (
    <button
      type="button"
      className="copy-button"
      data-copied={copied}
      onClick={handleCopy}
      aria-label={label}
    >
      {copied ? 'Copied' : 'Copy'}
    </button>
  );
}
