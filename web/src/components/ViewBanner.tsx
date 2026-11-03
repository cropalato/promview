import type { ViewMode, ViewSummary } from '../alerts/view';

export interface ViewBannerProps {
  /** What the server reported it did to the list. Absent where the caller has no view. */
  summary: ViewSummary | undefined;
  /** The mode the list was asked for, which is what the button flips. */
  mode: ViewMode;
  onToggle: () => void;
}

/**
 * Says what the automatic view is doing to the list, and is the way out of it.
 *
 * A filter that hides alerts without saying so is how an on-call operator
 * misses one, so the indicator stays on screen for as long as a view exists:
 * it names the mode, counts what is held back, and offers the one-click
 * reversal. It renders nothing where there is no view to speak of (open mode,
 * or a caller whose groups give them nothing narrower than their scope), so it
 * never occupies the screen to say that nothing is happening.
 *
 * Switching here is for the moment. It does not rewrite the saved preference;
 * that lives in the view menu.
 */
export function ViewBanner({ summary, mode, onToggle }: ViewBannerProps) {
  if (summary === undefined || !summary.active) {
    return null;
  }
  const showingAll = mode === 'all';
  return (
    <section className="view-banner" aria-label="Automatic view">
      {/* Only the mode is announced. The count moves with every live refresh,
          and reading it aloud each time would drown the list it describes. */}
      <span className="view-banner-mode" role="status">
        {showingAll ? 'Showing everything in your scope' : 'Showing your view'}
      </span>
      <span className="view-banner-count">
        {showingAll
          ? summary.hidden === 0
            ? 'Your view would hide nothing right now'
            : `Your view would hide ${String(summary.hidden)}`
          : summary.hidden === 0
            ? 'Nothing is hidden'
            : `${String(summary.hidden)} hidden by your view`}
      </span>
      <button type="button" className="button view-banner-action" onClick={onToggle}>
        {showingAll ? 'Back to my view' : 'Show everything in my scope'}
      </button>
    </section>
  );
}
