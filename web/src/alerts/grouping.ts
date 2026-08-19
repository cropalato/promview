/**
 * The console's grouping vocabulary.
 *
 * This mirrors the API's closed set of grouping keys (`GroupKeys` in
 * internal/alerts/model.go): every key becomes a GROUP BY expression
 * server-side, so it cannot be just any label. The backend may grow that
 * vocabulary later (arbitrary label grouping is the expected direction);
 * when it does, extending the one list here is what makes the new keys
 * available everywhere in the console — the view menu, stored-preference
 * validation, and group headings all derive from it, so current and future
 * support stay in one place.
 */

export interface GroupKeyDefinition {
  id: string;
  label: string;
}

/** Keys the API accepts today, in the order the view menu offers them. */
export const GROUP_KEYS: readonly GroupKeyDefinition[] = [
  { id: 'alertname', label: 'Alert name' },
  { id: 'source', label: 'Source' },
  { id: 'team', label: 'Team' },
  { id: 'severity', label: 'Severity' },
  { id: 'instance', label: 'Instance' },
];

/**
 * Matches the API's MaxGroupKeys: past a few keys grouping stops collapsing
 * anything and only costs an aggregation.
 */
export const MAX_GROUP_KEYS = 3;

/** The out-of-the-box grouping: name and source collapse a fan-out. */
export const DEFAULT_GROUP_KEYS: readonly string[] = ['alertname', 'source'];

export function isGroupKey(id: string): boolean {
  return GROUP_KEYS.some((key) => key.id === id);
}

/** Display label for a key; unknown ids (a newer backend's keys) show as-is. */
export function groupKeyLabel(id: string): string {
  return GROUP_KEYS.find((key) => key.id === id)?.label ?? id;
}

/** A named combination the view menu offers as a one-click choice. */
export interface GroupingPreset {
  id: string;
  label: string;
  keys: readonly string[];
}

export const GROUPING_PRESETS: readonly GroupingPreset[] = [
  { id: 'name-source', label: 'Alert name and source', keys: ['alertname', 'source'] },
  { id: 'name', label: 'Alert name', keys: ['alertname'] },
  { id: 'source', label: 'Source', keys: ['source'] },
  { id: 'team', label: 'Team', keys: ['team'] },
  { id: 'severity', label: 'Severity', keys: ['severity'] },
];

function sameKeys(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((value, index) => value === b[index]);
}

/** The preset exactly matching these keys, or undefined for a custom set. */
export function presetForKeys(keys: readonly string[]): GroupingPreset | undefined {
  return GROUPING_PRESETS.find((preset) => sameKeys(preset.keys, keys));
}

/**
 * Coerces a key list into one the API accepts: known keys only, no
 * duplicates, at most MAX_GROUP_KEYS, and never empty — grouping with no
 * keys is rejected server-side, so an unusable list falls back rather than
 * silently ungrouping the console. Applied both to stored preferences (a
 * layout written by another version) and to view-menu edits.
 */
export function sanitizeGroupKeys(
  keys: readonly unknown[],
  fallback: readonly string[] = DEFAULT_GROUP_KEYS,
): string[] {
  const accepted: string[] = [];
  for (const key of keys) {
    if (typeof key !== 'string' || !isGroupKey(key) || accepted.includes(key)) {
      continue;
    }
    accepted.push(key);
    if (accepted.length === MAX_GROUP_KEYS) {
      break;
    }
  }
  return accepted.length > 0 ? accepted : [...fallback];
}

/**
 * A group's key entries in display order: the operator's grouping order
 * first, then any keys the server sent beyond it. The JSON object order
 * cannot be trusted for this — the Go server marshals maps with sorted
 * keys, which is alphabetical, not the order it grouped by.
 */
export function orderedGroupEntries(
  key: Record<string, string>,
  order: readonly string[],
): [string, string][] {
  const entries: [string, string][] = [];
  for (const name of order) {
    if (name in key) {
      entries.push([name, key[name] ?? '']);
    }
  }
  for (const [name, value] of Object.entries(key)) {
    if (!order.includes(name)) {
      entries.push([name, value]);
    }
  }
  return entries;
}
