import { useMemo } from 'react';
import { CopyButton } from './CopyButton';

interface AlertRawViewProps {
  rawData: unknown;
}

/**
 * Raw tab: the source payload pretty-printed as JSON. Rendered as text
 * content only — never through innerHTML — so payload markup stays inert.
 */
export function AlertRawView({ rawData }: AlertRawViewProps) {
  const formatted = useMemo(() => {
    try {
      return JSON.stringify(rawData ?? {}, null, 2);
    } catch {
      // Unserializable payloads (BigInt, cycles) degrade to a plain string.
      return String(rawData);
    }
  }, [rawData]);

  return (
    <div className="detail-raw">
      <div className="detail-raw-actions">
        <CopyButton value={formatted} label="Copy raw payload" />
      </div>
      <pre className="detail-raw-pre">
        <code>{formatted}</code>
      </pre>
    </div>
  );
}
