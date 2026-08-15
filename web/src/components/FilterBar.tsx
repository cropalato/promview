import { useEffect, useRef } from 'react';
import type { FormEvent, KeyboardEvent } from 'react';
import { FilterIcon } from './icons';

interface FilterBarProps {
  value: string;
  shown: number;
  total: number;
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
 * Prometheus-style label filter. Currently a client-side text match over the
 * alerts loaded in the browser; pressing `/` anywhere focuses it, Enter
 * applies, Escape clears. Server-side filtering replaces the local matcher
 * in a later phase.
 */
export function FilterBar({ value, shown, total, onChange, onApply }: FilterBarProps) {
  const inputRef = useRef<HTMLInputElement>(null);

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
    <form className="filter-bar" role="search" aria-label="Alert filter" onSubmit={handleSubmit}>
      <FilterIcon className="filter-icon" />
      <input
        ref={inputRef}
        className="filter-input"
        type="text"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={handleKeyDown}
        placeholder='{severity="critical", team=~"infra.*"}'
        aria-label="Filter alerts by label expression"
        aria-keyshortcuts="/"
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
  );
}
