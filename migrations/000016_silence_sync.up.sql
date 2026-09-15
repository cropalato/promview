-- The silence table stops being promview's own diary and becomes the inventory
-- of what each source Alertmanager holds, synced every reconcile pass.
--
-- Provenance alone could not answer the question an operator actually asks of a
-- dimmed row: "is this silence still real?" A silence deleted straight on the
-- Alertmanager left promview's copy — and the suppressed flags derived from it —
-- frozen at whatever the last webhook happened to say. Syncing the listing
-- closes that: silences made outside promview gain a record with their real
-- author, and ones that vanish are marked expired instead of lingering.
--
-- `state` is Alertmanager's own word (active, pending, expired), verbatim so a
-- newer version's state is stored rather than mistranslated. Existing rows
-- predate the sync; their expiry time is the best available guess until the
-- first pass corrects them.
ALTER TABLE alertmanager_silences
    ADD COLUMN state text NOT NULL DEFAULT 'active',
    ADD COLUMN matcher_list jsonb NOT NULL DEFAULT '[]'::jsonb;

UPDATE alertmanager_silences SET state = 'expired' WHERE ends_at < now();

-- Rows written before this migration hold only the equality map promview
-- itself writes, so the full-fidelity list is derived from it losslessly.
UPDATE alertmanager_silences SET matcher_list = COALESCE(
    (
        SELECT jsonb_agg(
            jsonb_build_object('name', pair.key, 'value', pair.value,
                               'isRegex', false, 'isEqual', true)
            ORDER BY pair.key
        )
        FROM jsonb_each_text(matchers) AS pair
    ),
    '[]'::jsonb
);
