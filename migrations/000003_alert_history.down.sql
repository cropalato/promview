DROP TABLE alert_history;

ALTER TABLE alerts
    DROP COLUMN raw_data,
    DROP COLUMN occurrence;
