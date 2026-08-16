import type { JSX } from 'react';
import type { Severity } from '../alerts/severity';

interface IconProps {
  className?: string;
}

function a11y(className?: string) {
  return { className, 'aria-hidden': true as const, focusable: false as const };
}

/** Product mark: a waveform pulse, evoking the live alert stream. */
export function PulseMark({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 64 64" {...a11y(className)}>
      <path
        d="M8 36h12l6-16 10 28 6-18 4 6h10"
        fill="none"
        stroke="currentColor"
        strokeWidth={5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/** Severity glyphs double as shape coding (octagon/triangle/circle), so
 *  severity never depends on color alone. */
export function OctagonIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <path d="M7.9 2h8.2L22 7.9v8.2L16.1 22H7.9L2 16.1V7.9Z" fill="currentColor" />
    </svg>
  );
}

export function TriangleIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <path
        d="M12 3 23 21H1Z"
        fill="currentColor"
        stroke="currentColor"
        strokeWidth={2}
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function CircleIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <circle cx={12} cy={12} r={9} fill="currentColor" />
    </svg>
  );
}

export function SeverityIcon({
  severity,
  className,
}: {
  severity: Severity;
  className?: string;
}): JSX.Element {
  switch (severity) {
    case 'critical':
      return <OctagonIcon className={className} />;
    case 'warning':
      return <TriangleIcon className={className} />;
    case 'info':
      return <CircleIcon className={className} />;
  }
}

export function FilterIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <path
        d="M3 5h18l-7 8v6l-4-2v-4L3 5Z"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function UserIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <circle cx={12} cy={8} r={4} fill="none" stroke="currentColor" strokeWidth={1.8} />
      <path
        d="M4 21c1.5-4 5-5.5 8-5.5s6.5 1.5 8 5.5"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinecap="round"
      />
    </svg>
  );
}

/** Bell for the browser-notification opt-in toggle. */
export function BellIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <path
        d="M6 10a6 6 0 0 1 12 0c0 4 1.5 5.5 2 6H4c.5-.5 2-2 2-6Z"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinejoin="round"
      />
      <path
        d="M10 19a2 2 0 0 0 4 0"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinecap="round"
      />
    </svg>
  );
}

/** Slashed bell for the off/blocked notification states. */
export function BellOffIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <path
        d="M8.2 5.6A6 6 0 0 1 18 10c0 2.6.7 4.1 1.2 4.8M6 10.3c-.1 3.6-1.5 5-2 5.7h11"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="M10 19a2 2 0 0 0 4 0"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinecap="round"
      />
      <path d="M4 4l16 16" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
    </svg>
  );
}

/** Dismiss glyph for the detail drawer/sheet. */
export function CloseIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <path
        d="M6 6l12 12M18 6L6 18"
        fill="none"
        stroke="currentColor"
        strokeWidth={2}
        strokeLinecap="round"
      />
    </svg>
  );
}

/** Box-and-arrow glyph marking links that leave the console. */
export function ExternalLinkIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <path
        d="M14 4h6v6M20 4l-9 9"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="M10 5H5a1 1 0 0 0-1 1v13a1 1 0 0 0 1 1h13a1 1 0 0 0 1-1v-5"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/** Quiet radar: rings plus a sweep, used by the no-alert state. */
export function RadarIcon({ className }: IconProps): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" {...a11y(className)}>
      <circle cx={12} cy={12} r={9} fill="none" stroke="currentColor" strokeWidth={1.6} />
      <circle
        cx={12}
        cy={12}
        r={5}
        fill="none"
        stroke="currentColor"
        strokeWidth={1.2}
        opacity={0.5}
      />
      <path d="M12 12 18 6" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" />
      <circle cx={15.5} cy={8.5} r={1.4} fill="currentColor" />
    </svg>
  );
}
