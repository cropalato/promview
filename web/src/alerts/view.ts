/**
 * The automatic view: the filter a caller's own scoped groups put on their
 * console. The server owns it. It derives the selectors from the caller's
 * bindings, applies them inside the caller's scope, and reports what it did;
 * the console only says which view it wants and shows the result. A view can
 * narrow what scope allows and never widens it, so nothing here is a security
 * boundary.
 */

/** `mine` applies the view; `all` shows everything the caller's scope allows. */
export type ViewMode = 'mine' | 'all';

/** Why an alert is in front of the caller, relative to their view. */
export type ViewMatch = 'view' | 'critical' | 'outside';

/**
 * What the console asks the server for.
 *
 * Only an explicit `override` travels as a parameter. Without one the server
 * uses the saved preference itself, which keeps every ordinary request exactly
 * as it was and means the console never imposes its guess about the preference
 * (the local copy, before the server's has loaded) over the real one. `mode` and
 * `criticalBypass` are what the request amounts to; they ride along so that a
 * change in either is a different request and refetches the list.
 */
export interface ViewRequest {
  mode: ViewMode;
  criticalBypass: boolean;
  /** The moment's choice from the banner, or null to leave it to the saved preference. */
  override: ViewMode | null;
  /**
   * Counts the changes to the caller's view groups made from this console. The
   * server derives the view from membership, which the request does not carry,
   * so a join or leave has to read as a different request to refetch and to
   * reopen the stream from a new snapshot.
   */
  revision: number;
}

/**
 * What the server reports it did to a result. Absent where the caller has no
 * user to keep a view for (open mode).
 */
export interface ViewSummary {
  /** The caller has view selectors. False means there is nothing to show or toggle. */
  active: boolean;
  /** The result was not narrowed by the view. */
  showingAll: boolean;
  /**
   * Alerts in scope the view excludes under the same other filters. When
   * `showingAll`, what the view would hide.
   */
  hidden: number;
  criticalBypass: boolean;
}

export function isViewMode(value: unknown): value is ViewMode {
  return value === 'mine' || value === 'all';
}

export function isViewMatch(value: unknown): value is ViewMatch {
  return value === 'view' || value === 'critical' || value === 'outside';
}

/**
 * Content key for a request. Part of every query key, so a change refetches,
 * and what a stream connection is opened under, so a change reconnects.
 */
export function viewKeyOf(view: ViewRequest | undefined): string {
  return view === undefined
    ? ''
    : `${view.mode}:${view.criticalBypass ? 'bypass' : 'filtered'}:${view.revision}`;
}

/**
 * Resolves what to ask for. `override` is the moment's choice, made from the
 * banner, and is never saved: it stands in for the saved switch only until the
 * operator changes their mind or reloads. An override that equals the saved
 * choice is no override at all.
 */
export function resolveViewRequest(
  saved: { enabled: boolean; criticalBypass: boolean },
  override: ViewMode | null,
  revision = 0,
): ViewRequest {
  const savedMode: ViewMode = saved.enabled ? 'mine' : 'all';
  return { mode: override ?? savedMode, criticalBypass: saved.criticalBypass, override, revision };
}

/** The override that flips the current mode, collapsing to null when it is the saved one. */
export function toggledOverride(saved: { enabled: boolean }, current: ViewMode): ViewMode | null {
  const next: ViewMode = current === 'mine' ? 'all' : 'mine';
  const savedMode: ViewMode = saved.enabled ? 'mine' : 'all';
  return next === savedMode ? null : next;
}

/** Parses the `view` block of a list response, or undefined when absent or malformed. */
export function parseViewSummary(value: unknown): ViewSummary | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return undefined;
  }
  const raw = value as Record<string, unknown>;
  if (typeof raw.active !== 'boolean') {
    return undefined;
  }
  return {
    active: raw.active,
    showingAll: raw.showingAll === true,
    hidden: typeof raw.hidden === 'number' && Number.isFinite(raw.hidden) ? raw.hidden : 0,
    // Defaults to true, which is what the server does for a caller who never chose.
    criticalBypass: raw.criticalBypass !== false,
  };
}
