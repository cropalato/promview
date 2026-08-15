import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { alertDetailPath, alertIdFromPath, useAlertRoute } from './useAlertRoute';

beforeEach(() => {
  window.history.replaceState(null, '', '/');
});

afterEach(() => {
  window.history.replaceState(null, '', '/');
});

describe('alertIdFromPath', () => {
  it('extracts and decodes the id from detail paths', () => {
    expect(alertIdFromPath('/alerts/42')).toBe('42');
    expect(alertIdFromPath('/alerts/42/')).toBe('42');
    expect(alertIdFromPath('/alerts/a%20b')).toBe('a b');
  });

  it('returns null for non-detail paths', () => {
    expect(alertIdFromPath('/')).toBeNull();
    expect(alertIdFromPath('/alerts')).toBeNull();
    expect(alertIdFromPath('/alerts/')).toBeNull();
    expect(alertIdFromPath('/other/42')).toBeNull();
  });

  it('round-trips ids through alertDetailPath', () => {
    expect(alertDetailPath('42')).toBe('/alerts/42');
    expect(alertIdFromPath(alertDetailPath('a b/c'))).toBe('a b/c');
  });
});

describe('useAlertRoute', () => {
  it('starts without a selection on the list route', () => {
    const { result } = renderHook(() => useAlertRoute());

    expect(result.current.selectedAlertId).toBeNull();
  });

  it('restores the selection from a direct detail URL', () => {
    window.history.replaceState(null, '', '/alerts/42');
    const { result } = renderHook(() => useAlertRoute());

    expect(result.current.selectedAlertId).toBe('42');
  });

  it('pushes a history entry when opening an alert', () => {
    const { result } = renderHook(() => useAlertRoute());

    act(() => result.current.openAlert('42'));

    expect(result.current.selectedAlertId).toBe('42');
    expect(window.location.pathname).toBe('/alerts/42');
  });

  it('replaces the detail entry when closing, so back never reopens it', () => {
    const { result } = renderHook(() => useAlertRoute());
    act(() => result.current.openAlert('42'));

    act(() => result.current.closeAlert());

    expect(result.current.selectedAlertId).toBeNull();
    expect(window.location.pathname).toBe('/');
  });

  it('follows back/forward navigation via popstate', () => {
    const { result } = renderHook(() => useAlertRoute());
    act(() => result.current.openAlert('42'));

    // Back: the location returns to the list route and the selection clears.
    act(() => {
      window.history.replaceState(null, '', '/');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(result.current.selectedAlertId).toBeNull();

    // Forward: the detail route restores the selection.
    act(() => {
      window.history.replaceState(null, '', '/alerts/42');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(result.current.selectedAlertId).toBe('42');
  });

  it('switches the selection when another alert is opened', () => {
    const { result } = renderHook(() => useAlertRoute());
    act(() => result.current.openAlert('1'));
    act(() => result.current.openAlert('2'));

    expect(result.current.selectedAlertId).toBe('2');
    expect(window.location.pathname).toBe('/alerts/2');
  });
});
