-- 006_return_request_replacement.sql
-- Adds replacement_material_id to return_requests so exchange approvals can
-- validate that the replacement item has sufficient inventory.
ALTER TABLE return_requests
    ADD COLUMN replacement_material_id INTEGER REFERENCES materials(id);
