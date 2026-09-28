-- Keep the user-facing app label alongside its stable GitHub/ZIP identity.
ALTER TABLE services ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE deployment_jobs ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
