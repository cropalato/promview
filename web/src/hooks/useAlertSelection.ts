import { useCallback, useMemo, useState } from 'react';
import type { AlertSummary } from '../alerts/types';
import type { AlertSelection } from '../components/AlertTable';

/**
 * Checkbox selection over the loaded page.
 *
 * The selection survives the stream-driven refreshes that arrive constantly on
 * a busy console: an operator ticking forty rows while alerts keep landing must
 * not have the work emptied under them. Ids that leave the page entirely are
 * dropped, because acting on an alert the operator can no longer see is not
 * what they selected.
 */
export function useAlertSelection(alerts: readonly AlertSummary[]): {
  selection: AlertSelection;
  selectedIds: string[];
  clear: () => void;
} {
  const [selected, setSelected] = useState<ReadonlySet<string>>(() => new Set());

  const loadedIds = useMemo(() => new Set(alerts.map((alert) => alert.id)), [alerts]);

  // A refresh that drops rows drops their selection with them. Rebuilding only
  // when something actually left keeps the identity stable, so the table does
  // not re-render on every poll that changed nothing.
  const [prunedFor, setPrunedFor] = useState(loadedIds);
  if (prunedFor !== loadedIds) {
    setPrunedFor(loadedIds);
    setSelected((current) => {
      let changed = false;
      const next = new Set<string>();
      for (const id of current) {
        if (loadedIds.has(id)) {
          next.add(id);
        } else {
          changed = true;
        }
      }
      return changed ? next : current;
    });
  }

  const onToggle = useCallback((id: string) => {
    setSelected((current) => {
      const next = new Set(current);
      if (!next.delete(id)) {
        next.add(id);
      }
      return next;
    });
  }, []);

  const allSelected = alerts.length > 0 && alerts.every((alert) => selected.has(alert.id));

  const onToggleAll = useCallback(() => {
    setSelected((current) => {
      const everySelected = alerts.length > 0 && alerts.every((alert) => current.has(alert.id));
      return everySelected ? new Set() : new Set(alerts.map((alert) => alert.id));
    });
  }, [alerts]);

  const clear = useCallback(() => setSelected(new Set()), []);

  const selection = useMemo<AlertSelection>(
    () => ({ selectedIds: selected, allSelected, onToggle, onToggleAll }),
    [selected, allSelected, onToggle, onToggleAll],
  );

  // Ordered by the page rather than by tick order, so the request and any
  // report about it read in the order the operator is looking at.
  const selectedIds = useMemo(
    () => alerts.filter((alert) => selected.has(alert.id)).map((alert) => alert.id),
    [alerts, selected],
  );

  return { selection, selectedIds, clear };
}
