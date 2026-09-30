-- Token lifetimes and last use (older rows keep NULL = no expiry).
ALTER TABLE members ADD COLUMN expires_at TEXT;
ALTER TABLE api_keys ADD COLUMN expires_at TEXT;
ALTER TABLE api_keys ADD COLUMN last_used_at TEXT;
