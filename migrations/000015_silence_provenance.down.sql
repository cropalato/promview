DROP TABLE alertmanager_silences;

ALTER TABLE alerts DROP COLUMN silenced_by;
