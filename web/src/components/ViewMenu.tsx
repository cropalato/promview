import { useEffect, useId, useRef, useState } from 'react';
import { FIXED_COLUMNS, LABEL_COLUMN_PREFIX } from '../alerts/columns';
import { DENSITIES } from '../preferences/store';
import type { Density, Preferences } from '../preferences/store';
import { GroupingEditor } from './GroupingEditor';

/**
 * The controls for how the console shows alerts: grouped or flat and by which
 * keys, how dense the table is, and which columns it keeps.
 *
 * Everything here writes straight through to preferences, which is what carries
 * the choice to the operator's other machines. A column can be bound to any
 * alert label, which is the escape hatch for a dimension the built-in columns
 * do not cover. Grouping keys come from the API's closed vocabulary instead:
 * each one becomes a server-side GROUP BY, so the picker in GroupingEditor is
 * limited to what the endpoint accepts.
 */

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
}

export function ViewMenu({
  preferences,
  onChange,
  labelSuggestions = [],
  resolvedDensity,
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

  const labelColumns = preferences.columns.filter((column) =>
    column.id.startsWith(LABEL_COLUMN_PREFIX),
  );
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
            {FIXED_COLUMNS.map((column) => (
              <label key={column.id} className="view-menu-check">
                <input
                  type="checkbox"
                  checked={selected.has(column.id)}
                  onChange={() => toggleColumn(column.id)}
                />
                <span>{column.label}</span>
              </label>
            ))}
            {labelColumns.map((column) => (
              <label key={column.id} className="view-menu-check">
                <input type="checkbox" checked onChange={() => toggleColumn(column.id)} />
                <span>{column.id.slice(LABEL_COLUMN_PREFIX.length)}</span>
              </label>
            ))}
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
