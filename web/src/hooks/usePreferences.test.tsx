import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defaultPreferences } from '../preferences/store';
import { usePreferences } from './usePreferences';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('usePreferences', () => {
  it('reports a save in flight, so what the server derives from it can wait', async () => {
    // The server applies the saved view itself. A request made while the save
    // that changed it is still travelling would read the copy from before.
    let finishSave: (response: Response) => void = () => {};
    const fetchMock = vi.fn((_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        return new Promise<Response>((resolve) => {
          finishSave = resolve;
        });
      }
      return Promise.resolve(jsonResponse(defaultPreferences()));
    });
    vi.stubGlobal('fetch', fetchMock);

    const { result } = renderHook(() => usePreferences(true, true));
    await waitFor(() => expect(result.current.origin).toBe('server'));
    expect(result.current.saving).toBe(false);

    act(() => {
      result.current.update({
        ...result.current.preferences,
        view: { enabled: false, criticalBypass: true },
      });
    });
    // The change is applied locally at once; the save is still outstanding.
    expect(result.current.preferences.view.enabled).toBe(false);
    expect(result.current.saving).toBe(true);

    await act(async () => {
      finishSave(jsonResponse({}));
    });
    await waitFor(() => expect(result.current.saving).toBe(false));
  });
});
