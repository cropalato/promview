import { useEffect, useState } from 'react';

function pad(value: number): string {
  return String(value).padStart(2, '0');
}

/** Ticking UTC clock — a small ops-console staple for correlating events. */
export function UtcClock() {
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  const text = `${pad(now.getUTCHours())}:${pad(now.getUTCMinutes())}:${pad(now.getUTCSeconds())} UTC`;

  return (
    <time className="clock" dateTime={now.toISOString()} title="Coordinated Universal Time">
      {text}
    </time>
  );
}
