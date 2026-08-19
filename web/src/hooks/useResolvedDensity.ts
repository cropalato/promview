import { useEffect, useState } from 'react';
import { resolveDensity } from '../preferences/density';
import type { ResolvedDensity } from '../preferences/density';
import type { Density } from '../preferences/store';

/**
 * Turns the stored density preference into the one the table renders at,
 * re-measuring when the window changes so moving a window between screens or
 * resizing a split view lands on the right row height without a reload.
 *
 * An explicit preference short-circuits the measurement entirely: an operator
 * who chose compact keeps compact on every screen.
 */
export function useResolvedDensity(preference: Density): ResolvedDensity {
  const [height, setHeight] = useState(() =>
    typeof window === 'undefined' ? 0 : window.innerHeight,
  );

  useEffect(() => {
    if (preference !== 'auto' || typeof window === 'undefined') {
      return;
    }
    const measure = () => setHeight(window.innerHeight);
    measure();
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, [preference]);

  return resolveDensity(preference, height);
}
