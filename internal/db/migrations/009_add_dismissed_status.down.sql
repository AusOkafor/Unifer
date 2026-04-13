-- Revert any dismissed rows to pending before dropping the constraint
UPDATE duplicate_groups SET status = 'pending' WHERE status = 'dismissed';

ALTER TABLE duplicate_groups
    DROP CONSTRAINT IF EXISTS duplicate_groups_status_check;

ALTER TABLE duplicate_groups
    ADD CONSTRAINT duplicate_groups_status_check
    CHECK (status IN ('pending', 'reviewed', 'merged'));
