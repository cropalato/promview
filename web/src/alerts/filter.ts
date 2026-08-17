/**
 * Parser for the Prometheus-style label matcher syntax used by the alert
 * filter. Accepted forms:
 *
 *   severity="critical"
 *   team!="infra"
 *   severity="critical", team!="infra"
 *   {severity="critical", team!="infra"}
 *
 * Matchers support positive (`=`) and negative (`!=`) operators; values are
 * double-quoted strings in the input. The parsed matchers are serialized one
 * per repeated `match` query parameter (`team=core`, `team!=core`) and
 * filtered server-side (see `buildAlertsUrl`), so the filter covers every
 * alert the query matches, not just the rows loaded in the browser.
 */
export type MatchOperator = '=' | '!=';

export interface LabelMatcher {
  name: string;
  op: MatchOperator;
  value: string;
}

/** Parse failure with the 0-based offset into the input for display. */
export class FilterParseError extends Error {
  readonly position: number;

  constructor(message: string, position: number) {
    super(`${message} (column ${position + 1})`);
    this.name = 'FilterParseError';
    this.position = position;
  }
}

const LABEL_NAME = /^[a-zA-Z_][a-zA-Z0-9_]*/;

/**
 * Parses a filter expression into label matchers. Empty input (or empty
 * braces) yields an empty list. Throws `FilterParseError` on malformed input.
 */
export function parseFilter(input: string): LabelMatcher[] {
  const source = input.trim();
  if (source === '') {
    return [];
  }
  const parser = new FilterParser(source);
  return parser.parse();
}

/**
 * Wire form sent as one repeated `match` query parameter, e.g. `team!=core`.
 * The endpoint reads everything after the operator as the raw label value,
 * so the value travels unquoted.
 */
export function serializeMatcher(matcher: LabelMatcher): string {
  return `${matcher.name}${matcher.op}${matcher.value}`;
}

/** Quoted display form of one matcher, e.g. `team!="core"`. */
export function formatMatcher(matcher: LabelMatcher): string {
  return `${matcher.name}${matcher.op}"${escapeValue(matcher.value)}"`;
}

/**
 * Canonical filter expression for a matcher set: empty for no matchers, a
 * bare matcher for one, brace-wrapped comma-separated form otherwise. Used to
 * reflect programmatic filter changes (label include/exclude buttons) back
 * into the filter input.
 */
export function formatFilter(matchers: readonly LabelMatcher[]): string {
  if (matchers.length === 0) {
    return '';
  }
  if (matchers.length === 1 && matchers[0] !== undefined) {
    return formatMatcher(matchers[0]);
  }
  return `{${matchers.map(formatMatcher).join(', ')}}`;
}

/**
 * Replaces the matcher for the same label name when one exists, otherwise
 * appends. This is the upsert behind the detail drawer's include/exclude
 * buttons: one constraint per label keeps the filter expression predictable.
 */
export function upsertMatcher(
  matchers: readonly LabelMatcher[],
  next: LabelMatcher,
): LabelMatcher[] {
  const index = matchers.findIndex((matcher) => matcher.name === next.name);
  if (index === -1) {
    return [...matchers, next];
  }
  const updated = [...matchers];
  updated[index] = next;
  return updated;
}

/** Quote a decoded value the way Prometheus string literals expect. */
function escapeValue(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\t/g, '\\t');
}

class FilterParser {
  private pos = 0;

  constructor(private readonly source: string) {}

  parse(): LabelMatcher[] {
    this.skipSpace();
    const braced = this.peek() === '{';
    if (braced) {
      this.pos += 1;
    }
    const matchers = this.parseList(braced);
    if (braced) {
      this.skipSpace();
      if (this.peek() !== '}') {
        throw this.error('expected "," or "}" after matcher');
      }
      this.pos += 1;
    }
    this.skipSpace();
    if (!this.atEnd()) {
      throw this.error(
        braced ? 'unexpected input after closing "}"' : 'expected "," between matchers',
      );
    }
    return matchers;
  }

  private parseList(braced: boolean): LabelMatcher[] {
    const matchers: LabelMatcher[] = [];
    for (;;) {
      this.skipSpace();
      if (this.atEnd()) {
        if (matchers.length === 0 && !braced) {
          throw this.error('expected a label matcher');
        }
        // Lenient: an empty or trailing comma list is fine.
        return matchers;
      }
      if (braced && this.peek() === '}') {
        return matchers;
      }
      matchers.push(this.parseMatcher());
      this.skipSpace();
      if (this.peek() !== ',') {
        return matchers;
      }
      this.pos += 1;
    }
  }

  private parseMatcher(): LabelMatcher {
    this.skipSpace();
    const nameMatch = LABEL_NAME.exec(this.source.slice(this.pos));
    if (nameMatch === null) {
      throw this.error('expected a label name');
    }
    const name = nameMatch[0];
    this.pos += name.length;
    this.skipSpace();
    const op = this.parseOperator();
    this.skipSpace();
    const value = this.parseValue();
    return { name, op, value };
  }

  private parseOperator(): MatchOperator {
    const rest = this.source.slice(this.pos, this.pos + 2);
    if (rest === '=~' || rest === '!~') {
      throw this.error('regex matchers (=~ and !~) are not supported; use = or !=');
    }
    if (rest === '!=') {
      this.pos += 2;
      return rest;
    }
    if (this.peek() === '=') {
      this.pos += 1;
      return '=';
    }
    throw this.error('expected one of = or != after the label name');
  }

  private parseValue(): string {
    if (this.peek() !== '"') {
      throw this.error('expected a quoted label value, e.g. ="critical"');
    }
    this.pos += 1;
    let value = '';
    for (;;) {
      if (this.atEnd()) {
        throw this.error('unterminated quoted label value');
      }
      const char = this.source[this.pos] as string;
      this.pos += 1;
      if (char === '"') {
        return value;
      }
      if (char === '\\') {
        value += this.parseEscape();
      } else {
        value += char;
      }
    }
  }

  private parseEscape(): string {
    if (this.atEnd()) {
      throw this.error('unterminated escape in label value');
    }
    const char = this.source[this.pos] as string;
    this.pos += 1;
    switch (char) {
      case 'n':
        return '\n';
      case 't':
        return '\t';
      case '\\':
        return '\\';
      case '"':
        return '"';
      default:
        // Unknown escapes survive untouched so the server sees the same text.
        return `\\${char}`;
    }
  }

  private skipSpace(): void {
    while (!this.atEnd() && /\s/.test(this.source[this.pos] as string)) {
      this.pos += 1;
    }
  }

  private peek(): string | undefined {
    return this.source[this.pos];
  }

  private atEnd(): boolean {
    return this.pos >= this.source.length;
  }

  private error(message: string): FilterParseError {
    return new FilterParseError(message, this.pos);
  }
}
