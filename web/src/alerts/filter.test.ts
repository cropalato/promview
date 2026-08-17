import { describe, expect, it } from 'vitest';
import {
  FilterParseError,
  formatFilter,
  formatMatcher,
  parseFilter,
  serializeMatcher,
  upsertMatcher,
} from './filter';
import type { LabelMatcher } from './filter';

describe('parseFilter', () => {
  it('parses a bare positive matcher', () => {
    expect(parseFilter('severity="critical"')).toEqual([
      { name: 'severity', op: '=', value: 'critical' },
    ]);
  });

  it('parses a bare negative matcher', () => {
    expect(parseFilter('team!="infra"')).toEqual([{ name: 'team', op: '!=', value: 'infra' }]);
  });

  it('rejects regex operators with a targeted message', () => {
    expect(() => parseFilter('team=~"infra.*"')).toThrowError(/regex matchers.*not supported/);
    expect(() => parseFilter('instance!~"api-.*"')).toThrowError(/regex matchers.*not supported/);
  });

  it('parses a comma-separated list without braces', () => {
    expect(parseFilter('severity="critical", team!="infra"')).toEqual([
      { name: 'severity', op: '=', value: 'critical' },
      { name: 'team', op: '!=', value: 'infra' },
    ]);
  });

  it('parses a brace-wrapped list', () => {
    expect(parseFilter('{severity="critical", team!="infra"}')).toEqual([
      { name: 'severity', op: '=', value: 'critical' },
      { name: 'team', op: '!=', value: 'infra' },
    ]);
  });

  it('tolerates whitespace around every token', () => {
    expect(parseFilter('  {  severity = "critical" ,  team != "infra" }  ')).toEqual([
      { name: 'severity', op: '=', value: 'critical' },
      { name: 'team', op: '!=', value: 'infra' },
    ]);
  });

  it('parses an empty input or empty braces to no matchers', () => {
    expect(parseFilter('')).toEqual([]);
    expect(parseFilter('   ')).toEqual([]);
    expect(parseFilter('{}')).toEqual([]);
    expect(parseFilter('{ }')).toEqual([]);
  });

  it('tolerates a trailing comma', () => {
    expect(parseFilter('{severity="critical",}')).toEqual([
      { name: 'severity', op: '=', value: 'critical' },
    ]);
  });

  it('decodes escape sequences in values', () => {
    expect(parseFilter('summary="a \\"quoted\\" value"')).toEqual([
      { name: 'summary', op: '=', value: 'a "quoted" value' },
    ]);
    expect(parseFilter('path="C:\\\\tmp"')).toEqual([{ name: 'path', op: '=', value: 'C:\\tmp' }]);
  });

  it('supports underscores and digits in label names', () => {
    expect(parseFilter('http_status_2xx="true"')).toEqual([
      { name: 'http_status_2xx', op: '=', value: 'true' },
    ]);
  });

  it('rejects a bare word with a helpful position', () => {
    expect(() => parseFilter('critical')).toThrowError(FilterParseError);
    try {
      parseFilter('critical');
      expect.unreachable();
    } catch (error) {
      expect((error as FilterParseError).message).toMatch(/expected one of = or !=/);
      expect((error as FilterParseError).message).toMatch(/column 9/);
    }
  });

  it('rejects unquoted values', () => {
    expect(() => parseFilter('severity=critical')).toThrowError(/quoted label value/);
  });

  it('rejects unterminated values', () => {
    expect(() => parseFilter('severity="critical')).toThrowError(/unterminated/);
  });

  it('rejects unbalanced braces', () => {
    expect(() => parseFilter('{severity="critical"')).toThrowError(/expected "," or "}"/);
    expect(() => parseFilter('severity="critical"}')).toThrowError(/expected "," between matchers/);
  });

  it('rejects trailing input after closing braces', () => {
    expect(() => parseFilter('{severity="critical"} junk')).toThrowError(
      /unexpected input after closing "}"/,
    );
  });

  it('rejects leading digits in label names', () => {
    expect(() => parseFilter('2xx="true"')).toThrowError(/expected a label name/);
  });
});

describe('serializeMatcher', () => {
  it('serializes the unquoted wire form for the match param', () => {
    expect(serializeMatcher({ name: 'team', op: '=', value: 'core' })).toBe('team=core');
    expect(serializeMatcher({ name: 'team', op: '!=', value: 'core' })).toBe('team!=core');
    // Values travel raw; the endpoint reads everything after the operator.
    expect(serializeMatcher({ name: 'summary', op: '=', value: 'CPU > 90%' })).toBe(
      'summary=CPU > 90%',
    );
  });
});

describe('formatMatcher', () => {
  it('round-trips the quoted display form through the parser', () => {
    const matcher: LabelMatcher = { name: 'team', op: '!=', value: 'infra "eu"' };
    expect(formatMatcher(matcher)).toBe('team!="infra \\"eu\\""');
    expect(parseFilter(formatMatcher(matcher))).toEqual([matcher]);
  });
});

describe('formatFilter', () => {
  it('formats zero, one, and many matchers', () => {
    expect(formatFilter([])).toBe('');
    expect(formatFilter([{ name: 'team', op: '=', value: 'core' }])).toBe('team="core"');
    expect(
      formatFilter([
        { name: 'team', op: '=', value: 'core' },
        { name: 'severity', op: '!=', value: 'info' },
      ]),
    ).toBe('{team="core", severity!="info"}');
  });
});

describe('upsertMatcher', () => {
  const base: LabelMatcher[] = [
    { name: 'severity', op: '=', value: 'critical' },
    { name: 'team', op: '=', value: 'core' },
  ];

  it('appends a matcher for a new label', () => {
    expect(upsertMatcher(base, { name: 'instance', op: '=', value: 'api-1' })).toEqual([
      ...base,
      { name: 'instance', op: '=', value: 'api-1' },
    ]);
  });

  it('replaces an existing matcher in place, keeping order', () => {
    expect(upsertMatcher(base, { name: 'team', op: '!=', value: 'core' })).toEqual([
      { name: 'severity', op: '=', value: 'critical' },
      { name: 'team', op: '!=', value: 'core' },
    ]);
  });
});
