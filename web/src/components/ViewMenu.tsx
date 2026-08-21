import { useEffect, useId, useRef, useState } from 'react';
import { FIXED_COLUMNS, LABEL_COLUMN_PREFIX, columnFilterLabel } from '../alerts/columns';
import { DENSITIES } from '../preferences/store';
import type { ColumnPreference, Density, Preferences } from '../preferences/store';
import { GroupingEditor } from './GroupingEditor';

/**
 * The controls for how the console shows alerts: grouped or flat and by which
 * keys, how dense the table is, and which columns it keeps — and in which
 * order.
 *
 * Everything here writes straight through to preferences, which is what carries
 * the choice to the operator's other machines. A column can be bound to any
 * alert label, which is the escape hatch for a dimension the built-in columns
 * do not cover. Grouping keys come from the API's closed vocabulary instead:
 * each one becomes a server-side GROUP BY, so the picker in GroupingEditor is
 * limited to what the endpoint accepts.
 *
 * Column order was already what the table read; it just had no way in. The
 * kept columns are shown as an ordered list with per-row move buttons, the
 * same model as GroupingEditor: the order is the setting, and buttons keep it
 * reachable from a keyboard and readable to a screen reader in a way
 * drag-and-drop does not.
 */

/** How a kept column names itself in the menu. */
function columnLabel(id: string): string {
  if (id.startsWith(LABEL_COLUMN_PREFIX)) {
    return id.slice(LABEL_COLUMN_PREFIX.length);
  }
  return FIXED_COLUMNS.find((column) => column.id === id)?.label ?? id;
}

export interface ViewMenuProps {
  preferences: Preferences;
  onChange: (next: Preferences) => void;
  /** Label keys seen in the loaded alerts, offered as column suggestions. */
  labelSuggestions?: readonly string[];
  /**
   * What `auto` density currently resolves to, so choosing it does not leave
   * the operator guessing which row height they are about to get.
   */
  resolvedDensity?: string;
  /**
   * Label names the applied filter currently matches on. A column naming one of
   * these shows its filter button pressed.
   */
  filteredLabels?: readonly string[];
  /**
   * Starts a filter on a label: the console seeds the filter input with an
   * empty matcher and focuses it, since the menu knows the field but never the
   * value. Absent in a console with no filter bar, which hides the buttons.
   */
  onFilterLabel?: (name: string) => void;
  /** Drops the applied matcher on a label. */
  onClearLabelFilter?: (name: string) => void;
}

export function ViewMenu({
  preferences,
  onChange,
  labelSuggestions = [],
  resolvedDensity,
  filteredLabels = [],
  onFilterLabel,
  onClearLabelFilter,
}: ViewMenuProps) {
  const [open, setOpen] = useState(false);
  const [labelDraft, setLabelDraft] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const menuId = useId();

  useEffect(() => {
    if (!open) {
      return;
    }
    const handlePointer = (event: MouseEvent) => {
      if (containerRef.current !== null && !containerRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const handleKey = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', handlePointer);
    document.addEventListener('keydown', handleKey);
    return () => {
      document.removeEventListener('mousedown', handlePointer);
      document.removeEventListener('keydown', handleKey);
    };
  }, [open]);

  const selected = new Set(preferences.columns.map((column) => column.id));

  const toggleColumn = (id: string) => {
    const columns = selected.has(id)
      ? preferences.columns.filter((column) => column.id !== id)
      : [...preferences.columns, { id }];
    if (columns.length === 0) {
      // The table needs something to render; refuse rather than blank it.
      return;
    }
    onChange({ ...preferences, columns });
  };

  const addLabelColumn = () => {
    const name = labelDraft.trim();
    if (name === '') {
      return;
    }
    const id = `${LABEL_COLUMN_PREFIX}${name}`;
    setLabelDraft('');
    if (selected.has(id)) {
      return;
    }
    onChange({ ...preferences, columns: [...preferences.columns, { id }] });
  };

  // The buttons only exist where the console has somewhere to send them; a
  // consumer without a filter bar gets the menu without them.
  const canFilter = onFilterLabel !== undefined && onClearLabelFilter !== undefined;
  const filteredSet = new Set(filteredLabels);

  const fixedIds = new Set(FIXED_COLUMNS.map((column) => column.id));
  // The kept columns the menu can name, in saved order, each carrying where it
  // sits in the full list. Anything else — a column id a newer console saved —
  // has no row here but keeps its stored place; see moveColumn.
  const ordered = preferences.columns
    .map((column, index) => ({ column, index }))
    .filter(({ column }) =>
      column.id.startsWith(LABEL_COLUMN_PREFIX)
        ? column.id.length > LABEL_COLUMN_PREFIX.length
        : fixedIds.has(column.id),
    );

  const moveColumn = (position: number, delta: -1 | 1) => {
    const target = position + delta;
    if (target < 0 || target >= ordered.length) {
      return;
    }
    const from = ordered[position]?.index;
    const to = ordered[target]?.index;
    if (from === undefined || to === undefined) {
      return;
    }
    // Swap the stored entries whole rather than rebuilding them from ids: a
    // resized column keeps its width, and any field added later travels with
    // it for free. Swapping in place also leaves entries this list does not
    // show exactly where they were instead of dropping them.
    const columns = [...preferences.columns];
    const moved = columns[from] as ColumnPreference;
    columns[from] = columns[to] as ColumnPreference;
    columns[to] = moved;
    onChange({ ...preferences, columns });
  };

  // Fixed columns the operator has turned off. They have no place in the order
  // yet, so they sit below the list as the way back on; toggling one appends.
  const hiddenFixed = FIXED_COLUMNS.filter((column) => !selected.has(column.id));
  const suggestions = labelSuggestions.filter(
    (name) => !selected.has(`${LABEL_COLUMN_PREFIX}${name}`),
  );

  return (
    <div className="view-menu" ref={containerRef}>
      <button
        type="button"
        className="button"
        aria-expanded={open}
        aria-controls={menuId}
        onClick={() => setOpen((value) => !value)}
      >
        View
      </button>
      {open ? (
        <div className="view-menu-panel" id={menuId}>
          <fieldset className="view-menu-group">
            <legend>Grouping</legend>
            <label className="view-menu-check">
              <input
                type="checkbox"
                checked={preferences.grouping.enabled}
                onChange={(event) =>
                  onChange({
                    ...preferences,
                    grouping: { ...preferences.grouping, enabled: event.target.checked },
                  })
                }
              />
              <span>Group alerts</span>
            </label>
            {preferences.grouping.enabled ? (
              <GroupingEditor
                keys={preferences.grouping.keys}
                onChange={(keys) =>
                  onChange({ ...preferences, grouping: { ...preferences.grouping, keys } })
                }
              />
            ) : null}
          </fieldset>

          <fieldset className="view-menu-group">
            <legend>Density</legend>
            {DENSITIES.map((density: Density) => (
              <label key={density} className="view-menu-check">
                <input
                  type="radio"
                  name="density"
                  value={density}
                  checked={preferences.density === density}
                  onChange={() => onChange({ ...preferences, density })}
                />
                <span>
                  {density}
                  {density === 'auto' && resolvedDensity !== undefined ? (
                    <span className="view-menu-note"> · now {resolvedDensity}</span>
                  ) : null}
                </span>
              </label>
            ))}
          </fieldset>

          <fieldset className="view-menu-group">
            <legend>Columns</legend>
            <ul className="view-menu-columns" aria-label="Columns, in order">
              {ordered.map(({ column }, position) => {
                const label = columnLabel(column.id);
                // Only some columns name a label to match on; the rest keep
                // just their move buttons rather than a dead control.
                const filterName = columnFilterLabel(column.id);
                const filtered = filterName !== null && filteredSet.has(filterName);
                return (
                  <li key={column.id} className="view-menu-column">
                    <label className="view-menu-check">
                      <input type="checkbox" checked onChange={() => toggleColumn(column.id)} />
                      <span>{label}</span>
                    </label>
                    <span className="view-menu-column-actions">
                      {filterName !== null && canFilter ? (
                        <button
                          type="button"
                          className="button view-menu-column-action"
                          aria-label={
                            filtered ? `Remove the ${filterName} filter` : `Filter by ${filterName}`
                          }
                          aria-pressed={filtered}
                          title={
                            filtered ? `Remove the ${filterName} filter` : `Filter by ${filterName}`
                          }
                          onClick={() =>
                            filtered
                              ? onClearLabelFilter?.(filterName)
                              : onFilterLabel?.(filterName)
                          }
                        >
                          ⚑
                        </button>
                      ) : null}
                      <button
                        type="button"
                        className="button view-menu-column-action"
                        aria-label={`Move ${label} up`}
                        disabled={position === 0}
                        onClick={() => moveColumn(position, -1)}
                      >
                        ↑
                      </button>
                      <button
                        type="button"
                        className="button view-menu-column-action"
                        aria-label={`Move ${label} down`}
                        disabled={position === ordered.length - 1}
                        onClick={() => moveColumn(position, 1)}
                      >
                        ↓
                      </button>
                    </span>
                  </li>
                );
              })}
            </ul>
            {hiddenFixed.map((column) => (
              <label key={column.id} className="view-menu-check">
                <input type="checkbox" checked={false} onChange={() => toggleColumn(column.id)} />
                <span>{column.label}</span>
              </label>
            ))}
            {/* Moving a column changes the table without moving focus; say
                where the list landed. */}
            <p className="visually-hidden" role="status">
              Columns: {ordered.map(({ column }) => columnLabel(column.id)).join(', ')}
            </p>
          </fieldset>

          <fieldset className="view-menu-group">
            <legend>Add a label column</legend>
            <div className="view-menu-add">
              <input
                type="text"
                aria-label="Label name"
                placeholder="prometheus_cluster"
                list={`${menuId}-labels`}
                value={labelDraft}
                onChange={(event) => setLabelDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    event.preventDefault();
                    addLabelColumn();
                  }
                }}
              />
              <datalist id={`${menuId}-labels`}>
                {suggestions.map((name) => (
                  <option key={name} value={name} />
                ))}
              </datalist>
              <button type="button" className="button" onClick={addLabelColumn}>
                Add
              </button>
            </div>
          </fieldset>
        </div>
      ) : null}
    </div>
  );
}
