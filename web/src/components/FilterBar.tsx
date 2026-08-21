import { useEffect, useId, useRef } from 'react';
import type { FormEvent, KeyboardEvent } from 'react';
import { FilterIcon } from './icons';

interface FilterBarProps {
  value: string;
  shown: number;
  total: number;
  /**
   * Parse error for the current draft. The previous valid filter stays
   * applied server-side until the draft parses and is applied.
   */
  error?: string | null;
  onChange: (value: string) => void;
  onApply: (value: string) => void;
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  return target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable;
}

/**
 * Prometheus-style label filter (`severity="critical", team!="infra"`,
 * optionally brace-wrapped). Positive and negative matchers are applied
 * server-side: pressing `/` anywhere focuses the input, Enter applies,
 * Escape clears. A draft that does not parse shows the error inline and is
 * never sent to the API.
 */
export function FilterBar({
  value,
  shown,
  total,
  error = null,
  onChange,
  onApply,
}: FilterBarProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const errorId = useId();

  useEffect(() => {
    const handleGlobalKey = (event: globalThis.KeyboardEvent) => {
      if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey) {
        return;
      }
      if (isEditableTarget(event.target)) {
        return;
      }
      event.preventDefault();
      inputRef.current?.focus();
    };
    document.addEventListener('keydown', handleGlobalKey);
    return () => document.removeEventListener('keydown', handleGlobalKey);
  }, []);

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault();
    onApply(value);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      onApply(value);
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      onChange('');
      onApply('');
    }
  };

  return (
    <div className="filter-wrap">
      <form className="filter-bar" role="search" aria-label="Alert filter" onSubmit={handleSubmit}>
        <FilterIcon className="filter-icon" />
        <input
          ref={inputRef}
          className="filter-input"
          type="text"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={handleKeyDown}
          placeholder='{severity="critical", team!="infra"}'
          aria-label="Filter alerts by label expression"
          aria-keyshortcuts="/"
          aria-invalid={error !== null ? true : undefined}
          aria-describedby={error !== null ? errorId : undefined}
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="off"
          spellCheck={false}
        />
        <span className="filter-hint">
          <kbd>/</kbd> to focus
        </span>
        <output className="filter-count" aria-live="polite">
          {shown} of {total} alerts
        </output>
      </form>
      {error !== null ? (
        <p className="filter-error" role="alert" id={errorId}>
          {error}
        </p>
      ) : null}
    </div>
  );
}
