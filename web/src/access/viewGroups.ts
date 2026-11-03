/**
 * View groups against `/api/v1/view-groups`.
 *
 * A view group narrows what a user sees by default and is never a permission:
 * scope decides what a user may read, and the server intersects the two. Any
 * signed-in user lists, joins and leaves; defining and pinning is
 * administrator-only, and the server re-checks that on every request.
 */
import { AlertsApiError } from '../alerts/api';
import { send } from './bindings';
import type { BindingMatcher, SubjectKind } from './bindings';
import { apiFetch } from '../config/transport';

export const VIEW_GROUPS_URL = '/api/v1/view-groups';

/** Who a pin forces a group on: a user, or every member of a directory group. */
export interface ViewGroupPin {
  subjectKind: SubjectKind;
  userID?: number;
  subjectIssuer?: string;
  subjectGroup?: string;
}

export interface ViewGroup {
  name: string;
  description: string;
  joined: boolean;
  /** An administrator forced this group on the caller: it cannot be left. */
  pinned: boolean;
  /** Administrator-only: a selector can name labels the reader cannot see. */
  matchers: BindingMatcher[];
  pins: ViewGroupPin[];
  members: number;
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

function parsePins(value: unknown): ViewGroupPin[] {
  if (!Array.isArray(value)) {
    return [];
  }
  const pins: ViewGroupPin[] = [];
  for (const entry of value) {
    if (typeof entry !== 'object' || entry === null) {
      continue;
    }
    const raw = entry as Record<string, unknown>;
    // Each kind as itself: an LDAP pin shown as an OIDC one would name the
    // wrong directory to the administrator reading it.
    const kind: SubjectKind =
      raw.subjectKind === 'user' || raw.subjectKind === 'ldap_group'
        ? raw.subjectKind
        : 'oidc_group';
    pins.push({
      subjectKind: kind,
      userID: typeof raw.userID === 'number' ? raw.userID : undefined,
      subjectIssuer: typeof raw.subjectIssuer === 'string' ? raw.subjectIssuer : undefined,
      subjectGroup: typeof raw.subjectGroup === 'string' ? raw.subjectGroup : undefined,
    });
  }
  return pins;
}

function parseGroup(value: unknown): ViewGroup | null {
  if (typeof value !== 'object' || value === null) {
    return null;
  }
  const raw = value as Record<string, unknown>;
  if (typeof raw.name !== 'string' || raw.name === '') {
    return null;
  }
  return {
    name: raw.name,
    description: typeof raw.description === 'string' ? raw.description : '',
    joined: raw.joined === true,
    pinned: raw.pinned === true,
    matchers: parseMatchers(raw.matchers),
    pins: parsePins(raw.pins),
    members: typeof raw.members === 'number' ? raw.members : 0,
  };
}

/**
 * Lists every group with the caller's own state. A 404 is the server saying
 * there is no user to belong to a group (open mode), which is an empty list
 * here rather than an error.
 */
export async function fetchViewGroups(fetchImpl: FetchLike = apiFetch): Promise<ViewGroup[]> {
  let response: Response;
  try {
    response = await send(VIEW_GROUPS_URL, { method: 'GET' }, 'Listing view groups', fetchImpl);
  } catch (cause) {
    if (cause instanceof AlertsApiError && cause.status === 404) {
      return [];
    }
    throw cause;
  }
  let body: unknown;
  try {
    body = await response.json();
  } catch (cause) {
    throw new AlertsApiError('View groups response was not valid JSON', { cause });
  }
  const record = typeof body === 'object' && body !== null ? (body as Record<string, unknown>) : {};
  if (!Array.isArray(record.groups)) {
    return [];
  }
  const groups: ViewGroup[] = [];
  for (const entry of record.groups) {
    const group = parseGroup(entry);
    if (group !== null) {
      groups.push(group);
    }
  }
  return groups;
}

function groupUrl(name: string, suffix = ''): string {
  return `${VIEW_GROUPS_URL}/${encodeURIComponent(name)}${suffix}`;
}

export async function joinViewGroup(name: string, fetchImpl: FetchLike = apiFetch): Promise<void> {
  await send(groupUrl(name, '/membership'), { method: 'PUT' }, 'Joining the group', fetchImpl);
}

export async function leaveViewGroup(name: string, fetchImpl: FetchLike = apiFetch): Promise<void> {
  await send(groupUrl(name, '/membership'), { method: 'DELETE' }, 'Leaving the group', fetchImpl);
}

export async function saveViewGroup(
  group: { name: string; description: string; matchers: BindingMatcher[] },
  fetchImpl: FetchLike = apiFetch,
): Promise<void> {
  // The path names the group and the server refuses a body that disagrees.
  const { name, ...rest } = group;
  await send(
    groupUrl(name),
    {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(rest),
    },
    'Saving the group',
    fetchImpl,
  );
}

export async function deleteViewGroup(
  name: string,
  fetchImpl: FetchLike = apiFetch,
): Promise<void> {
  await send(groupUrl(name), { method: 'DELETE' }, 'Removing the group', fetchImpl);
}

export async function pinViewGroup(
  name: string,
  pin: ViewGroupPin,
  fetchImpl: FetchLike = apiFetch,
): Promise<void> {
  await send(
    groupUrl(name, '/pins'),
    { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(pin) },
    'Pinning the group',
    fetchImpl,
  );
}

export async function unpinViewGroup(
  name: string,
  pin: ViewGroupPin,
  fetchImpl: FetchLike = apiFetch,
): Promise<void> {
  await send(
    groupUrl(name, '/pins'),
    {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(pin),
    },
    'Unpinning the group',
    fetchImpl,
  );
}
