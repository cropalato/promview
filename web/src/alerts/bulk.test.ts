import { describe, expect, it, vi } from 'vitest';
import { MAX_BULK_ALERTS, bulkAcknowledge, bulkAssign, bulkClose, bulkNote } from './bulk';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const OK = {
  applied: 2,
  unchanged: 0,
  notFound: 0,
  results: [
    { id: 1, status: 'applied' },
    { id: 2, status: 'applied' },
  ],
};

describe('bulk actions', () => {
  it('sends string ids beside the single-alert body', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(jsonResponse(OK)));

    await bulkAcknowledge(['1', '2'], true, fetchImpl);
    expect(fetchImpl).toHaveBeenLastCalledWith('/api/v1/alerts/bulk/acknowledge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ids: ['1', '2'], acknowledged: true }),
    });

    await bulkAssign(['1'], 'platform-rota', fetchImpl);
    expect(fetchImpl).toHaveBeenLastCalledWith(
      '/api/v1/alerts/bulk/assignee',
      expect.objectContaining({ method: 'PUT' }),
    );

    await bulkClose(['1'], true, fetchImpl);
    expect(fetchImpl).toHaveBeenLastCalledWith(
      '/api/v1/alerts/bulk/close',
      expect.objectContaining({ body: JSON.stringify({ ids: ['1'], closed: true }) }),
    );

    await bulkNote(['1'], 'same root cause', fetchImpl);
    expect(fetchImpl).toHaveBeenLastCalledWith(
      '/api/v1/alerts/bulk/notes',
      expect.objectContaining({ body: JSON.stringify({ ids: ['1'], body: 'same root cause' }) }),
    );
  });

  it('normalises the numeric ids the API answers with', async () => {
    // The request takes strings and the reply gives numbers. Normalising once
    // here is cheaper than every caller remembering the asymmetry.
    const fetchImpl = vi.fn(() =>
      Promise.resolve(jsonResponse({ ...OK, results: [{ id: 42, status: 'applied' }] })),
    );
    const result = await bulkClose(['42'], true, fetchImpl);
    expect(result.results).toEqual([{ id: '42', status: 'applied' }]);
  });

  it('treats 207 as an answer that reports misses, not a failure', async () => {
    const fetchImpl = vi.fn(() =>
      Promise.resolve(
        jsonResponse(
          {
            applied: 1,
            unchanged: 1,
            notFound: 1,
            results: [
              { id: 1, status: 'applied' },
              { id: 2, status: 'unchanged' },
              { id: 3, status: 'notFound' },
            ],
          },
          207,
        ),
      ),
    );
    const result = await bulkClose(['1', '2', '3'], true, fetchImpl);
    expect(result.partial).toBe(true);
    expect([result.applied, result.unchanged, result.notFound]).toEqual([1, 1, 1]);
  });

  it('a clean run is not partial', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(jsonResponse(OK)));
    expect((await bulkAcknowledge(['1', '2'], true, fetchImpl)).partial).toBe(false);
  });

  it('refuses a selection the server would reject anyway', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(jsonResponse(OK)));
    await expect(bulkClose([], true, fetchImpl)).rejects.toThrow(/No alerts selected/);
    const tooMany = Array.from({ length: MAX_BULK_ALERTS + 1 }, (_, index) => String(index + 1));
    await expect(bulkClose(tooMany, true, fetchImpl)).rejects.toThrow(/At most 500/);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('surfaces a real failure', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(jsonResponse({}, 403)));
    await expect(bulkClose(['1'], true, fetchImpl)).rejects.toThrow(/HTTP 403/);
  });

  it('drops malformed entries rather than inventing outcomes', async () => {
    const fetchImpl = vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          applied: 1,
          results: [{ id: 1, status: 'applied' }, 'nonsense', { id: 0 }],
        }),
      ),
    );
    const result = await bulkClose(['1'], true, fetchImpl);
    expect(result.results).toEqual([{ id: '1', status: 'applied' }]);
  });
});
