-- Jalankan satu kali di Cloudflare D1 yang sudah memakai schema lama.
-- Hanya kolom URL aplikasi yang ditambahkan; tabel dan data tetap ada.
ALTER TABLE services ADD COLUMN app_url TEXT;
