import type { Density } from './store';

/**
 * Resolving `auto` density from the area the console actually has.
 *
 * The same operator moves between a laptop and a NOC wall display, so a single
 * stored row height is wrong on one of them. Height is what decides: density
 * exists to trade row height against how many alerts fit on screen, and a short
 * viewport wants compact rows while a tall one can afford to breathe.
 *
 * Viewport width is not part of the decision. Which columns survive a narrow
 * panel is answered by container queries in the stylesheet, against the panel's
 * own width rather than the window's - the console can sit in a split view or a
 * dashboard tile where those two disagree.
 */

/** A resolved density: what `auto` becomes, never `auto` itself. */
export type ResolvedDensity = Exclude<Density, 'auto'>;

/**
 * Height below which rows tighten, and above which they relax. The lower bound
 * is a 13-inch laptop with browser chrome; the upper is the point where a
 * display is being read from across a room rather than at arm's length.
 */
export const COMPACT_BELOW_PX = 720;
export const COMFORTABLE_ABOVE_PX = 1200;

/** Resolves a stored preference into the density the table should render at. */
export function resolveDensity(preference: Density, availableHeight: number): ResolvedDensity {
  if (preference !== 'auto') {
    return preference;
  }
  if (!Number.isFinite(availableHeight) || availableHeight <= 0) {
    // Nothing measured yet (first paint, or a headless environment): normal is
    // the safe middle, and the real answer replaces it once measured.
    return 'normal';
  }
  if (availableHeight < COMPACT_BELOW_PX) {
    return 'compact';
  }
  if (availableHeight >= COMFORTABLE_ABOVE_PX) {
    return 'comfortable';
  }
  return 'normal';
}
