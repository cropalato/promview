import { useEffect, useRef, useState } from 'react';
import {
  GROUP_KEYS,
  GROUPING_PRESETS,
  MAX_GROUP_KEYS,
  groupKeyLabel,
  presetForKeys,
} from '../alerts/grouping';

/**
 * The grouping-key picker inside the view menu.
 *
 * Presets cover the combinations an operator usually wants; "Custom" opens an
 * ordered list of the keys in use with add, remove, and reorder controls.
 * Reordering is buttons rather than drag-and-drop because the order is the
 * setting: it decides nesting and heading order, and buttons keep that
 * reachable from a keyboard and readable to a screen reader.
 *
 * Everything writes the whole key list through `onChange`, which the view
 * menu persists straight into preferences — there is no draft to apply.
 */

export interface GroupingEditorProps {
  /** Active grouping keys, in order. */
  keys: readonly string[];
  onChange: (keys: string[]) => void;
}

export function GroupingEditor({ keys, onChange }: GroupingEditorProps) {
  // Whether the custom editor is open. It is local (not derived from the
  // keys) so picking "Custom" over a preset-matching list keeps that list as
  // the starting point instead of forcing a divergence first.
  const [customizing, setCustomizing] = useState(false);
  const [addDraft, setAddDraft] = useState('');
  const addSelectRef = useRef<HTMLSelectElement>(null);
  // Set by a removal; the add control may only exist after the re-render
  // (removing at the key cap re-shows it), so focus lands in an effect.
  const [focusAdd, setFocusAdd] = useState(false);

  useEffect(() => {
    if (focusAdd && addSelectRef.current !== null) {
      addSelectRef.current.focus();
      setFocusAdd(false);
    }
  }, [focusAdd]);

  const preset = presetForKeys(keys);
  const customSelected = customizing || preset === undefined;
  const remaining = GROUP_KEYS.filter((key) => !keys.includes(key.id));
  const atMax = keys.length >= MAX_GROUP_KEYS;
  const canAdd = !atMax && remaining.length > 0;
  const draft = remaining.some((key) => key.id === addDraft) ? addDraft : (remaining[0]?.id ?? '');

  const move = (index: number, delta: -1 | 1) => {
    const target = index + delta;
    if (target < 0 || target >= keys.length) {
      return;
    }
    const next = [...keys];
    const [item] = next.splice(index, 1);
    next.splice(target, 0, item as string);
    onChange(next);
  };

  const remove = (index: number) => {
    // The API rejects grouping with no keys, so the last one stays.
    if (keys.length <= 1) {
      return;
    }
    onChange(keys.filter((_, position) => position !== index));
    // The button that had focus is gone; the add control is the stable
    // landing spot rather than dropping focus back to the menu top.
    setFocusAdd(true);
  };

  const add = () => {
    if (draft === '' || keys.includes(draft) || atMax) {
      return;
    }
    onChange([...keys, draft]);
  };

  return (
    <div className="grouping-editor">
      <div className="grouping-presets" role="radiogroup" aria-label="Grouping preset">
        {GROUPING_PRESETS.map((option) => (
          <label key={option.id} className="view-menu-check">
            <input
              type="radio"
              name="grouping-preset"
              checked={!customSelected && preset?.id === option.id}
              onChange={() => {
                setCustomizing(false);
                onChange([...option.keys]);
              }}
            />
            <span>{option.label}</span>
          </label>
        ))}
        <label className="view-menu-check">
          <input
            type="radio"
            name="grouping-preset"
            checked={customSelected}
            onChange={() => setCustomizing(true)}
          />
          <span>Custom</span>
        </label>
      </div>

      {customSelected ? (
        <div className="grouping-custom">
          <ul className="grouping-keys" aria-label="Grouping keys, in order">
            {keys.map((id, index) => {
              const label = groupKeyLabel(id);
              return (
                <li key={id} className="grouping-key">
                  <span className="grouping-key-name">{label}</span>
                  <span className="grouping-key-actions">
                    <button
                      type="button"
                      className="button grouping-key-action"
                      aria-label={`Move ${label} up`}
                      disabled={index === 0}
                      onClick={() => move(index, -1)}
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      className="button grouping-key-action"
                      aria-label={`Move ${label} down`}
                      disabled={index === keys.length - 1}
                      onClick={() => move(index, 1)}
                    >
                      ↓
                    </button>
                    <button
                      type="button"
                      className="button grouping-key-action"
                      aria-label={`Remove ${label} from grouping`}
                      disabled={keys.length <= 1}
                      onClick={() => remove(index)}
                    >
                      ✕
                    </button>
                  </span>
                </li>
              );
            })}
          </ul>
          {canAdd ? (
            <div className="view-menu-add">
              <select
                ref={addSelectRef}
                aria-label="Grouping key to add"
                value={draft}
                onChange={(event) => setAddDraft(event.target.value)}
              >
                {remaining.map((key) => (
                  <option key={key.id} value={key.id}>
                    {key.label}
                  </option>
                ))}
              </select>
              <button type="button" className="button" onClick={add}>
                Add
              </button>
            </div>
          ) : (
            <p className="view-menu-note">
              {atMax
                ? `The API groups by at most ${MAX_GROUP_KEYS} keys.`
                : 'Every available key is in use.'}
            </p>
          )}
          {/* Reorder/add/remove change meaning without moving focus; say where
              the list landed. */}
          <p className="visually-hidden" role="status">
            Grouped by {keys.map((id) => groupKeyLabel(id)).join(', then ')}
          </p>
        </div>
      ) : null}
    </div>
  );
}
