/**
 * Silencing an alert on its Alertmanager.
 *
 * This is the only action in the console that hides alerts rather than
 * surfacing them, and the only one that writes to a system promview does not
 * own. The server decides who may do it, who it is attributed to, and how long
 * it may last; the console's job is to say clearly what is about to be silenced
 * and for how long, and to report honestly when only part of it worked.
 */

export const ALERT_SILENCE_URL = (id: string) => `/api/v1/alerts/${encodeURIComponent(id)}/silence`;
export const GROUP_SILENCE_URL = '/api/v1/groups/silence';

/** What happened at one Alertmanager. A group can span several. */
export interface SilenceResult {
  source: string;
  silenceId?: string;
  error?: string;
}

export interface SilenceResponse {
  endsAt: string;
  createdBy: string;
  results: SilenceResult[];
}

export interface SilenceRequest {
  durationSeconds: number;
  comment: string;
}

export class SilenceError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'SilenceError';
    this.status = status;
  }
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

const browserFetch: FetchLike = (url, init) => fetch(url, { credentials: 'same-origin', ...init });

async function postSilence(
  url: string,
  body: unknown,
  fetchImpl: FetchLike,
): Promise<SilenceResponse> {
  let response: Response;
  try {
    response = await fetchImpl(url, {
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
    throw new SilenceError(message, response.status);
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
  fetchImpl: FetchLike = browserFetch,
): Promise<SilenceResponse> {
  return postSilence(ALERT_SILENCE_URL(id), request, fetchImpl);
}

export function silenceGroup(
  groupBy: readonly string[],
  key: Record<string, string>,
  request: SilenceRequest,
  fetchImpl: FetchLike = browserFetch,
): Promise<SilenceResponse> {
  return postSilence(GROUP_SILENCE_URL, { groupBy: [...groupBy], key, ...request }, fetchImpl);
}
