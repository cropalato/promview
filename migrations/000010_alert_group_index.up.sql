-- Expanding a group re-queries the alerts endpoint with an equality matcher on
-- alertname, and that predicate lands on labels->>'alertname'. The gin index on
-- labels answers containment and key-existence, not an extracted-key equality,
-- so without this the expand path scans every alert: measured on 20k alerts,
-- 4.8ms sequential against 0.245ms through this index.
--
-- The grouped aggregation itself is deliberately not the target. It touches most
-- rows, so PostgreSQL correctly prefers a hash aggregate over a sequential scan
-- (13ms for 20k alerts) and no index changes that.
CREATE INDEX alerts_alertname_source_idx ON alerts ((labels->>'alertname'), source_slug);
