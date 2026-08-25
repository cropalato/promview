/**
 * Silencing an alert on its Alertmanager.
 *
 * This is the only action in the console that hides alerts rather than
 * surfacing them, and the only one that writes to a system promview does not
 * own. The server decides who may do it, who it is attributed to, and how long
 * it may last; the console's job is to say clearly what is about to be silenced
 * and for how long, and to report honestly when only part of it worked.
 */

import { apiUrl } from '../config/apiBase';
import { apiFetch } from '../config/transport';

export const ALERT_SILENCE_URL = (id: string) => `/api/v1/alerts/${encodeURIComponent(id)}/silence`;
export const GROUP_SILENCE_URL = '/api/v1/groups/silence';
export const GROUP_SILENCE_PREVIEW_URL = '/api/v1/groups/silence/preview';

/** What happened at one Alertmanager. A group can span several. */
export interface SilenceResult {
  source: string;
  silenceId?: string;
  /** The exact match written here, which can differ between Alertmanagers. */
  matchers: Record<string, string>;
  members: number;
  error?: string;
}

export interface SilenceResponse {
  endsAt: string;
  createdBy: string;
  results: SilenceResult[];
}

/**
 * What a group silence would actually match, resolved by the server.
 *
 * The console cannot work this out: the match is every label the group's
 * firing members agree on, which is narrower than the grouping key and is only
 * knowable from the members themselves. Showing the key instead would
 * understate what is about to be hidden — a group keyed on `alertname` alone
 * would read as "silence this alert here" while silencing it everywhere.
 */
export interface SilenceTargetPreview {
  source: string;
  matchers: Record<string, string>;
  members: number;
}

export interface SilencePreview {
  /** What every target agrees on; each target's own match is at least this narrow. */
  matchers: Record<string, string>;
  memberCount: number;
  targets: SilenceTargetPreview[];
}

export interface SilenceRequest {
  durationSeconds: number;
  comment: string;
}

export class SilenceError extends Error {
  readonly status: number;
  /**
   * The match the server resolved, when it refused because the group moved
   * under the operator. Carrying it back lets the dialog show the new scope
   * instead of asking them to start again blind.
   */
  readonly matchers?: Record<string, string>;

  constructor(message: string, status: number, matchers?: Record<string, string>) {
    super(message);
    this.name = 'SilenceError';
    this.status = status;
    this.matchers = matchers;
  }
}

/** True when the group changed between the preview and the confirmation. */
export function isSilenceConflict(error: unknown): boolean {
  return error instanceof SilenceError && error.status === 409;
}

/** Duration choices offered in the dialog, in seconds. */
export const SILENCE_DURATIONS: readonly { seconds: number; label: string }[] = [
  { seconds: 30 * 60, label: '30 minutes' },
  { seconds: 60 * 60, label: '1 hour' },
  { seconds: 2 * 60 * 60, label: '2 hours' },
  { seconds: 4 * 60 * 60, label: '4 hours' },
  { seconds: 12 * 60 * 60, label: '12 hours' },
  { seconds: 24 * 60 * 60, label: '1 day' },
  { seconds: 7 * 24 * 60 * 60, label: '1 week' },
];

/**
 * The choices this deployment allows, with its own default guaranteed present.
 * A configured default of 90 minutes is not in the fixed list, and offering a
 * dialog that cannot express the deployment's own default would be absurd.
 */
export function silenceDurationOptions(
  defaultSeconds: number,
  maxSeconds: number,
): { seconds: number; label: string }[] {
  const options = SILENCE_DURATIONS.filter((option) => option.seconds <= maxSeconds);
  if (!options.some((option) => option.seconds === defaultSeconds)) {
    options.push({ seconds: defaultSeconds, label: formatDuration(defaultSeconds) });
  }
  return options.sort((left, right) => left.seconds - right.seconds);
}

export function formatDuration(seconds: number): string {
  if (seconds % (24 * 60 * 60) === 0) {
    const days = seconds / (24 * 60 * 60);
    return days === 1 ? '1 day' : `${days} days`;
  }
  if (seconds % (60 * 60) === 0) {
    const hours = seconds / (60 * 60);
    return hours === 1 ? '1 hour' : `${hours} hours`;
  }
  const minutes = Math.round(seconds / 60);
  return minutes === 1 ? '1 minute' : `${minutes} minutes`;
}

/**
 * The transport seam the desktop client needs: a Tauri shell owns credentials
 * in the Rust core and keeps them out of the webview, so it supplies its own
 * caller rather than inheriting the browser's cookie jar. Defaults to a
 * same-origin browser fetch, which is what the console itself uses.
 */
type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

async function postSilence(
  url: string,
  body: unknown,
  fetchImpl: FetchLike,
): Promise<SilenceResponse> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(url), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  } catch {
    throw new SilenceError('Unable to reach the Promview API', 0);
  }
  let payload: unknown = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  // 207 is a partial success: some Alertmanagers took the silence and some did
  // not. It is not an error, and the per-target results carry which is which.
  if (!response.ok && response.status !== 207) {
    const message =
      typeof payload === 'object' &&
      payload !== null &&
      typeof (payload as { error?: unknown }).error === 'string'
        ? (payload as { error: string }).error
        : `Silence failed (HTTP ${response.status})`;
    throw new SilenceError(message, response.status, parseMatchers(payload));
  }
  return parseSilenceResponse(payload);
}

export function parseSilenceResponse(payload: unknown): SilenceResponse {
  const raw = (typeof payload === 'object' && payload !== null ? payload : {}) as Record<
    string,
    unknown
  >;
  const results = Array.isArray(raw.results)
    ? raw.results.flatMap((entry): SilenceResult[] => {
        if (typeof entry !== 'object' || entry === null) {
          return [];
        }
        const item = entry as Record<string, unknown>;
        return [
          {
            source: typeof item.source === 'string' ? item.source : 'unknown',
            silenceId: typeof item.silenceId === 'string' ? item.silenceId : undefined,
            matchers: labelRecord(item.matchers),
            members:
              typeof item.members === 'number' && Number.isFinite(item.members) ? item.members : 0,
            error: typeof item.error === 'string' ? item.error : undefined,
          },
        ];
      })
    : [];
  return {
    endsAt: typeof raw.endsAt === 'string' ? raw.endsAt : '',
    createdBy: typeof raw.createdBy === 'string' ? raw.createdBy : '',
    results,
  };
}

export function silenceAlert(
  id: string,
  request: SilenceRequest,
  fetchImpl: FetchLike = apiFetch,
): Promise<SilenceResponse> {
  return postSilence(ALERT_SILENCE_URL(id), request, fetchImpl);
}

export function silenceGroup(
  groupBy: readonly string[],
  key: Record<string, string>,
  request: SilenceRequest,
  expectedMatchers?: Record<string, string>,
  fetchImpl: FetchLike = apiFetch,
): Promise<SilenceResponse> {
  return postSilence(
    GROUP_SILENCE_URL,
    { groupBy: [...groupBy], key, expectedMatchers, ...request },
    fetchImpl,
  );
}

/** Asks the server what a group silence would match, before offering to write it. */
export async function previewGroupSilence(
  groupBy: readonly string[],
  key: Record<string, string>,
  fetchImpl: FetchLike = apiFetch,
): Promise<SilencePreview> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(GROUP_SILENCE_PREVIEW_URL), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ groupBy: [...groupBy], key }),
    });
  } catch {
    throw new SilenceError('Unable to reach the Promview API', 0);
  }
  let payload: unknown = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  if (!response.ok) {
    const message =
      typeof payload === 'object' &&
      payload !== null &&
      typeof (payload as { error?: unknown }).error === 'string'
        ? (payload as { error: string }).error
        : `Silence preview failed (HTTP ${response.status})`;
    throw new SilenceError(message, response.status);
  }
  return parseSilencePreview(payload);
}

export function parseSilencePreview(payload: unknown): SilencePreview {
  const raw = (typeof payload === 'object' && payload !== null ? payload : {}) as Record<
    string,
    unknown
  >;
  const targets = Array.isArray(raw.targets)
    ? raw.targets.flatMap((entry): SilenceTargetPreview[] => {
        if (typeof entry !== 'object' || entry === null) {
          return [];
        }
        const item = entry as Record<string, unknown>;
        return [
          {
            source: typeof item.source === 'string' ? item.source : 'unknown',
            matchers: labelRecord(item.matchers),
            members:
              typeof item.members === 'number' && Number.isFinite(item.members) ? item.members : 0,
          },
        ];
      })
    : [];
  return {
    matchers: labelRecord(raw.matchers),
    memberCount:
      typeof raw.memberCount === 'number' && Number.isFinite(raw.memberCount) ? raw.memberCount : 0,
    targets,
  };
}

function labelRecord(value: unknown): Record<string, string> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return {};
  }
  const result: Record<string, string> = {};
  for (const [name, entry] of Object.entries(value as Record<string, unknown>)) {
    if (typeof entry === 'string') {
      result[name] = entry;
    }
  }
  return result;
}

function parseMatchers(payload: unknown): Record<string, string> | undefined {
  if (typeof payload !== 'object' || payload === null) {
    return undefined;
  }
  const matchers = (payload as Record<string, unknown>).matchers;
  if (typeof matchers !== 'object' || matchers === null || Array.isArray(matchers)) {
    return undefined;
  }
  return labelRecord(matchers);
}
