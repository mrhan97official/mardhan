-- Only for an existing services table that lacks these legacy columns.
-- The migration runner skips an individual column when it already exists.
ALTER TABLE services ADD COLUMN repo TEXT;
ALTER TABLE services ADD COLUMN branch TEXT DEFAULT 'main';
