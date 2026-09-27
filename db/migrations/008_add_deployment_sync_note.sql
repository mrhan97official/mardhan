-- GitHub sync summary (files removed/kept) copied into the update history.
ALTER TABLE deployment_jobs ADD COLUMN sync_note TEXT NOT NULL DEFAULT '';
