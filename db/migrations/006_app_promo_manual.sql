-- Existing promotions retain their image and repository link until edited.
ALTER TABLE app_promo_banner ADD COLUMN app_name TEXT NOT NULL DEFAULT '';
ALTER TABLE app_promo_banner ADD COLUMN app_url TEXT NOT NULL DEFAULT '';
