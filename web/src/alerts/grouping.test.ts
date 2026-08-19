import { describe, expect, it } from 'vitest';
import {
  DEFAULT_GROUP_KEYS,
  GROUP_KEYS,
  GROUPING_PRESETS,
  MAX_GROUP_KEYS,
  orderedGroupEntries,
  presetForKeys,
  sanitizeGroupKeys,
} from './grouping';

describe('grouping vocabulary', () => {
  it('keeps the default grouping within what the API accepts', () => {
    // The default ships in every fresh preferences payload; if it ever fell
    // outside the vocabulary, a new console's first grouped request would 400.
    for (const key of DEFAULT_GROUP_KEYS) {
      expect(GROUP_KEYS.some((known) => known.id === key)).toBe(true);
    }
    expect(DEFAULT_GROUP_KEYS.length).toBeLessThanOrEqual(MAX_GROUP_KEYS);
  });

  it('builds presets only from known keys, within the API limit', () => {
    for (const preset of GROUPING_PRESETS) {
      expect(preset.keys.length).toBeGreaterThan(0);
      expect(preset.keys.length).toBeLessThanOrEqual(MAX_GROUP_KEYS);
      for (const key of preset.keys) {
        expect(GROUP_KEYS.some((known) => known.id === key)).toBe(true);
      }
    }
    // The default grouping must be reachable as a preset, or a fresh console
    // would open the view menu on "Custom" for a choice it never made.
    expect(presetForKeys(DEFAULT_GROUP_KEYS)).toBeDefined();
  });
});

describe('sanitizeGroupKeys', () => {
  it('keeps known keys in order', () => {
    expect(sanitizeGroupKeys(['team', 'alertname'])).toEqual(['team', 'alertname']);
  });

  it('drops keys the API cannot group by', () => {
    // A layout written by a newer console (or edited by hand) must not turn
    // every grouped request into a 400.
    expect(sanitizeGroupKeys(['alertname', 'prometheus_cluster'])).toEqual(['alertname']);
  });

  it('drops duplicates and non-strings', () => {
    expect(sanitizeGroupKeys(['team', 'team', 42, null])).toEqual(['team']);
  });

  it('caps the list at the API limit', () => {
    expect(sanitizeGroupKeys(['alertname', 'source', 'team', 'severity', 'instance'])).toEqual([
      'alertname',
      'source',
      'team',
    ]);
  });

  it('falls back when nothing usable remains', () => {
    expect(sanitizeGroupKeys(['nonsense'])).toEqual(DEFAULT_GROUP_KEYS);
    expect(sanitizeGroupKeys([])).toEqual(DEFAULT_GROUP_KEYS);
    expect(sanitizeGroupKeys(['nonsense'], ['severity'])).toEqual(['severity']);
  });
});

describe('presetForKeys', () => {
  it('matches a preset only on the exact ordered keys', () => {
    expect(presetForKeys(['alertname', 'source'])?.id).toBe('name-source');
    expect(presetForKeys(['team'])?.id).toBe('team');
    // Same keys, different nesting order: a custom grouping, not the preset.
    expect(presetForKeys(['source', 'alertname'])).toBeUndefined();
    expect(presetForKeys(['alertname', 'source', 'team'])).toBeUndefined();
  });
});

describe('orderedGroupEntries', () => {
  it('orders entries by the grouping order, not the payload order', () => {
    // The server marshals key maps with sorted keys, so the JSON order is
    // alphabetical even when the console grouped by something else.
    const key = { alertname: 'Cardinality', source: 'yul' };
    expect(orderedGroupEntries(key, ['source', 'alertname'])).toEqual([
      ['source', 'yul'],
      ['alertname', 'Cardinality'],
    ]);
  });

  it('appends keys the payload has beyond the grouping order', () => {
    const key = { alertname: 'Cardinality', team: 'core' };
    expect(orderedGroupEntries(key, ['team'])).toEqual([
      ['team', 'core'],
      ['alertname', 'Cardinality'],
    ]);
  });

  it('skips grouping keys absent from the payload', () => {
    expect(orderedGroupEntries({ alertname: 'Cardinality' }, ['alertname', 'source'])).toEqual([
      ['alertname', 'Cardinality'],
    ]);
  });
});
