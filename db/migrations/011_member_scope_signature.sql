-- Operator app scope ('*' = every app) and a signature over each member row.
ALTER TABLE members ADD COLUMN app_scope TEXT NOT NULL DEFAULT '*';
ALTER TABLE members ADD COLUMN sig TEXT NOT NULL DEFAULT '';
