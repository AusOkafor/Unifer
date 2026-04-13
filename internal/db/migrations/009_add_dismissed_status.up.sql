ALTER TABLE duplicate_groups
    DROP CONSTRAINT IF EXISTS duplicate_groups_status_check;

ALTER TABLE duplicate_groups
    ADD CONSTRAINT duplicate_groups_status_check
    CHECK (status IN ('pending', 'reviewed', 'merged', 'dismissed'));
