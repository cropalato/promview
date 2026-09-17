/**
 * Role binding administration against `/api/v1/access/bindings`.
 *
 * Administrator only. The server re-checks that on every request; this client
 * exists so the console can offer the view, not so it can decide the policy.
 */
import { AlertsApiError } from '../alerts/api';
import { apiUrl } from '../config/apiBase';
import { apiFetch } from '../config/transport';

export const BINDINGS_URL = '/api/v1/access/bindings';

/** Who a binding grants to: a Promview user, or an OIDC group. */
export type SubjectKind = 'user' | 'oidc_group';

export type BindingRole = 'viewer' | 'operator' | 'administrator';

export interface BindingMatcher {
  name: string;
  operator: string;
  value: string;
}

export interface RoleBinding {
  name: string;
  subjectKind: SubjectKind;
  userID?: number;
  oidcIssuer?: string;
  oidcGroup?: string;
  role: BindingRole;
  /**
   * The label scope, and half of what a binding means: a viewer bound with
   * `team=platform` sees a different deployment than one bound without it, so
   * the list shows these rather than the role alone.
   */
  matchers: BindingMatcher[];
}

type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

function parseMatchers(value: unknown): BindingMatcher[] {
  if (!Array.isArray(value)) {
    return [];
  }
  const matchers: BindingMatcher[] = [];
  for (const entry of value) {
    if (typeof entry !== 'object' || entry === null) {
      continue;
    }
    const raw = entry as Record<string, unknown>;
    if (typeof raw.name !== 'string' || raw.name === '') {
      continue;
    }
    matchers.push({
      name: raw.name,
      operator: typeof raw.operator === 'string' ? raw.operator : '=',
      value: typeof raw.value === 'string' ? raw.value : '',
    });
  }
  return matchers;
}

function parseBinding(value: unknown): RoleBinding | null {
  if (typeof value !== 'object' || value === null) {
    return null;
  }
  const raw = value as Record<string, unknown>;
  if (typeof raw.name !== 'string' || raw.name === '') {
    return null;
  }
  const role = raw.role;
  if (role !== 'viewer' && role !== 'operator' && role !== 'administrator') {
    return null;
  }
  return {
    name: raw.name,
    subjectKind: raw.subjectKind === 'user' ? 'user' : 'oidc_group',
    userID: typeof raw.userID === 'number' ? raw.userID : undefined,
    oidcIssuer: typeof raw.oidcIssuer === 'string' ? raw.oidcIssuer : undefined,
    oidcGroup: typeof raw.oidcGroup === 'string' ? raw.oidcGroup : undefined,
    role,
    matchers: parseMatchers(raw.matchers),
  };
}

async function send(
  url: string,
  init: RequestInit,
  what: string,
  fetchImpl: FetchLike,
): Promise<Response> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(url), init);
  } catch (cause) {
    throw new AlertsApiError('Unable to reach the Promview API', { cause });
  }
  if (!response.ok) {
    // The server answers 400 for a binding the caller can fix, including its
    // refusal to remove the last administrator, and puts the reason in the
    // body. Dropping it would leave an administrator guessing at a rule that
    // is trying to help them.
    let detail = '';
    try {
      const body = (await response.json()) as Record<string, unknown>;
      if (typeof body.error === 'string') {
        detail = body.error;
      }
    } catch {
      detail = '';
    }
    throw new AlertsApiError(detail !== '' ? detail : `${what} failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }
  return response;
}

export async function fetchRoleBindings(fetchImpl: FetchLike = apiFetch): Promise<RoleBinding[]> {
  const response = await send(BINDINGS_URL, { method: 'GET' }, 'Listing bindings', fetchImpl);
  let body: unknown;
  try {
    body = await response.json();
  } catch (cause) {
    throw new AlertsApiError('Bindings response was not valid JSON', { cause });
  }
  const record = typeof body === 'object' && body !== null ? (body as Record<string, unknown>) : {};
  if (!Array.isArray(record.bindings)) {
    return [];
  }
  const bindings: RoleBinding[] = [];
  for (const entry of record.bindings) {
    const binding = parseBinding(entry);
    if (binding !== null) {
      bindings.push(binding);
    }
  }
  return bindings;
}

export async function saveRoleBinding(
  binding: RoleBinding,
  fetchImpl: FetchLike = apiFetch,
): Promise<void> {
  // The path names the binding and the server refuses a body that disagrees,
  // so the name travels in the path alone.
  const { name, ...rest } = binding;
  await send(
    `${BINDINGS_URL}/${encodeURIComponent(name)}`,
    {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(rest),
    },
    'Saving the binding',
    fetchImpl,
  );
}

export async function deleteRoleBinding(
  name: string,
  fetchImpl: FetchLike = apiFetch,
): Promise<void> {
  await send(
    `${BINDINGS_URL}/${encodeURIComponent(name)}`,
    { method: 'DELETE' },
    'Removing the binding',
    fetchImpl,
  );
}

/**
 * Selector operators a binding may carry. Wider than the console's filter bar,
 * which is `=` and `!=` only: a binding written through the CLI or the API can
 * carry a regex scope, and an editor that understood four operators as two
 * would silently rewrite somebody's scope into a narrower one.
 */
const SCOPE_OPERATORS = ['!~', '=~', '!=', '='] as const;

/** Renders a scope the way it is typed, e.g. `team=platform, env!~^dev`. */
export function formatScope(matchers: readonly BindingMatcher[]): string {
  return matchers.map((matcher) => `${matcher.name}${matcher.operator}${matcher.value}`).join(', ');
}

/**
 * Parses a comma-separated scope. Returns null for anything malformed rather
 * than a partial scope: half a scope is a different binding, not a smaller one.
 */
export function parseScope(input: string): BindingMatcher[] | null {
  const source = input.trim();
  if (source === '') {
    return [];
  }
  const matchers: BindingMatcher[] = [];
  for (const part of source.split(',')) {
    const clause = part.trim();
    if (clause === '') {
      continue;
    }
    // Longest first, so `!=` is never read as `!` followed by `=`, and `=~`
    // never as a bare `=`.
    const operator = SCOPE_OPERATORS.find((candidate) => clause.includes(candidate));
    if (operator === undefined) {
      return null;
    }
    const at = clause.indexOf(operator);
    const name = clause.slice(0, at).trim();
    const value = clause.slice(at + operator.length).trim();
    if (name === '' || value === '') {
      return null;
    }
    matchers.push({ name, operator, value });
  }
  return matchers;
}
