-- Error location, cause, fixes and AI prompt for failed deployments.
ALTER TABLE deployment_jobs ADD COLUMN diagnosis TEXT NOT NULL DEFAULT '';
