# DevControl

Replika dashboard **DEV CONTROL** (infrastructure & deployment workspace) dari desain yang diberikan, dibangun sebagai aplikasi **offline-first PWA**.

## Arsitektur

```
┌─────────────────────────┐        ┌────────────────────────────┐
│   Next.js (App Router)  │  HTTP  │  Go serverless functions    │
│   Frontend + Service    │ ─────► │  (/api/*.go, Vercel Go      │
│   Worker + IndexedDB    │  JSON  │  runtime)                   │
└─────────────────────────┘        └──────────────┬───────────────┘
        ▲   offline cache                          │ REST API
        │   (IndexedDB via idb)                     ▼
        │                              ┌────────────────────────┐
        └──────────────────────────────│  Cloudflare D1 (SQLite) │
                                        └────────────────────────┘
```

- **Frontend**: Next.js 14 (App Router) + TypeScript + Tailwind CSS, disusun ulang persis dengan referensi desain (sidebar, stat card, deployment pipeline, environment status, infra health, API performance, services, recent activity, live logs), sepenuhnya responsif dari mobile sampai desktop lebar.
- **Backend**: Go, satu *serverless function* tunggal (`api/gateway.go`) di Vercel yang menangani semua endpoint (`/api/overview`, `/api/services`, dst) lewat routing internal berdasarkan query `?resource=`. `vercel.json` berisi `rewrites` yang memetakan tiap path publik ke `/api/gateway?resource=<nama>` tanpa mengubah URL yang dilihat browser/frontend. Backend tidak menyimpan state — setiap request meneruskan query ke Cloudflare D1 lewat **D1 REST API**, jadi tidak butuh driver SQLite native maupun koneksi database yang persisten (cocok untuk lingkungan serverless).
- **Database**: Cloudflare D1 (SQLite terdistribusi Cloudflare). Skema ada di `db/schema.sql`, data contoh di `db/seed.sql`.
- **Akses admin**: dashboard dan endpoint yang mengubah sistem dilindungi sesi admin HttpOnly. API key klien hanya memiliki hak GET terbatas. Saat offline, login dan data admin membutuhkan koneksi; respons API tidak disimpan di cache service worker.

## Struktur folder

```
devcontrol/
├── app/                # Next.js App Router (page, layout, offline fallback)
├── components/         # Semua komponen UI dashboard
├── lib/                # types, IndexedDB wrapper, hook offline-first, data fallback
├── api/
│   └── gateway.go       # SATU Go serverless function untuk semua endpoint (routed via vercel.json rewrites)
├── pkg/
│   ├── d1/              # Klien Cloudflare D1 REST API
│   └── util/             # Helper JSON/CORS response
├── db/
│   ├── schema.sql        # Empat belas tabel aplikasi dan metadata D1
│   ├── verify.sql        # Pemeriksaan empat belas tabel di konsol D1
│   ├── migrations/       # Kolom tambahan untuk skema lama
│   └── seed.sql           # Data contoh sesuai desain referensi
├── public/
│   └── icons/               # Ikon PWA bawaan (192/512/maskable + favicon)
├── wrangler.toml          # Opsional, hanya untuk `wrangler d1` CLI manual — app pakai CF_* env vars
└── vercel.json             # Header cache untuk sw.js/manifest/api
```

## Menjalankan secara lokal

### 1. Buat database Cloudflare D1

```bash
npm install -g wrangler
wrangler login

# Buat database — catat "database_id" yang muncul, dipakai di langkah 2 (bukan wrangler.toml)
wrangler d1 create devcontrol-db
```

### 2. Buat API Token Cloudflare & isi `.env.local`

Cloudflare dashboard → **My Profile → API Tokens → Create Token** → gunakan template *"Edit Cloudflare Workers"* (pastikan izin **D1: Edit** ikut, atau buat token custom dengan permission `Account.D1`).

Catat 3 nilai ini:
- **Account ID** (sidebar kanan halaman overview domain mana pun)
- **Database ID** (dari `wrangler d1 create` di atas, atau Workers & Pages → D1 → devcontrol-db)
- **API Token** (dari langkah di atas)

Untuk pemakaian lokal, salin `.env.example` menjadi `.env.local` lalu isi `CF_ACCOUNT_ID`, `CF_D1_DATABASE_ID`, `CF_API_TOKEN`. Tambahkan `DEVCONTROL_ADMIN_PASSWORD` (minimal 16 karakter), `DEVCONTROL_SESSION_SECRET` (acak minimal 32 karakter), dan nama satu bucket `CF_R2_BUCKET`; token Cloudflare perlu izin D1 Edit serta Workers R2 Storage Read/Write. Set juga semua variabel ini pada Environment Variables Vercel untuk lingkungan production, lalu deploy ulang. Database D1 dan token tetap dibuat satu kali di akun Cloudflare; aplikasi tidak dapat membuat kredensialnya sendiri.

```bash
# Buat seluruh tabel yang belum ada (aman dijalankan lagi jika baru satu tabel tercipta)
npm run db:migrate
# Verifikasi seluruh 14 tabel setelah penyiapan
npm run db:verify
# Hanya jika ingin data contoh; jangan jalankan dua kali
npm run db:seed
```

Setelah login, buka **Databases** untuk melihat tabel/kolom/indeks yang kurang. Tombol **Siapkan D1 + R2** menambah struktur yang belum ada, memeriksa atau membuat satu bucket privat, lalu menguji tulis/baca objek sementara. Jalankan tombol ini sekali sebelum memakai fitur lain; awal deployment juga mencoba menyiapkan penyimpanan otomatis. Penyiapan tidak menjalankan `db/seed.sql`, menghapus tabel, atau mengubah kolom tidak dikenal. `db/verify.sql` dan `npm run db:migrate` tetap tersedia sebagai alat pemulihan terminal. Bila skema lama memiliki perubahan khusus di luar migrasi yang tersedia, periksa selisihnya sebelum memodifikasi database production.

Untuk database lama, penyiapan menambahkan kolom `services.app_url`, `services.repo`, atau `services.branch` hanya jika belum ada. Migrasi lain menggunakan `CREATE IF NOT EXISTS`. Bila struktur tabel lain sudah diubah secara manual, UI akan melaporkan selisih dan tidak menebak cara memperbaikinya.

### API Management

Menu **API Management** menampilkan endpoint baca DevControl dan dapat mendaftarkan path GET/HEAD pada proyek yang sudah dideploy serta mempunyai `services.app_url`. Tombol **Uji sekarang** benar-benar memanggil URL HTTPS publik dan menyimpan kode status serta waktu respons di D1. Aktivasi/jeda hanya mengatur pemeriksaan oleh DevControl, bukan menghentikan API di aplikasi eksternal. Grafik menampilkan jumlah/latensi/galat pemeriksaan yang dijalankan, bukan seluruh trafik aplikasi lain. Untuk trafik penuh dibutuhkan instrumentasi aplikasi atau integrasi log terpisah.

Admin dapat membuat API key baca untuk scope yang dipilih dan mencabutnya kapan saja. Kunci hanya ditampilkan sekali; D1 menyimpan hash dan riwayat tindakan tanpa nilai rahasia. Contoh pemakaian klien: `Authorization: Bearer dc_...` pada `GET /api/overview` dengan scope `read:overview`. Tidak ada API key yang bisa memanggil deployment, update diri, migrasi, atau arsip ZIP.

### Infrastructure Health

Kartu **CPU** menampilkan estimasi pemakaian CPU instans Go yang melayani permintaan, dirata-ratakan sejak instans hidup; **Memory** menampilkan besar heap Go instans tersebut dalam MiB. Karena API berjalan di Vercel Functions, dua angka ini bukan penggunaan semua mesin atau seluruh deployment. Grafik CPU/Memory tidak dibuat jika belum ada riwayat pengukuran yang sebanding.

Untuk **Network** dan **Requests**, buka **Settings → Monitoring otomatis** lalu tekan **Aktifkan monitoring satu klik**. Backend memakai zona Cloudflare hanya jika tepat satu zona cocok dengan domain project dan analitiknya dapat dibaca. Jika tidak ada zona (misalnya domain `vercel.app`), backend mengaktifkan pencatatan permintaan dan byte respons **Go API DevControl** di D1. Kartu Overview menampilkan **API Network** dan **API Requests** dengan sumber serta cakupan: halaman, berkas statis, dan trafik CDN Vercel tidak termasuk. Ini merupakan pengukuran aktivitas API, bukan pengganti metrik trafik seluruh project Vercel. Grafik API mencakup jam berjalan dan 23 jam sebelumnya; pencatatan dimulai setelah tombol ditekan. Permintaan `/api/health` dikecualikan agar polling dashboard tidak menambah angka sendiri. Kegagalan pencatatan tidak menggagalkan aksi utama aplikasi. Jika ingin metrik seluruh hostname Cloudflare, hubungkan domain milik Anda ke proxy Cloudflare lalu pilih zona secara manual di pengaturan lanjutan. `CF_API_TOKEN` membutuhkan **Zone:Zone:Read** untuk menemukan zona dan **Account Analytics Read** untuk membaca metriknya. Persetujuan zona langsung aktif dari D1 dan menulis `CF_ZONE_ID` ke environment project Vercel dengan `VERCEL_TOKEN` untuk deployment berikutnya; jika penulisan Vercel gagal, Settings menawarkan percobaan ulang. Domain `vercel.app` sendiri tidak memiliki zona Cloudflare milik Anda. Data Cloudflare GraphQL mencakup semua hostname dalam zona dan diperbarui paling cepat tiap menit; totalnya dapat menggunakan sampling adaptif. Tabel contoh `infra_metrics` tidak digunakan sebagai metrik langsung.

Bila `db/schema.sql` diperbarui, jalankan `npm run db:generate` dan sertakan perubahan `pkg/setup/schema_generated.go` pada commit/ZIP agar fungsi Go menjalankan SQL yang sama. Hak akses database dan session tetap diperiksa di backend. Password admin serta secret sesi harus berbeda; mengganti secret sesi membatalkan seluruh cookie lama. Versi ini memakai satu admin instalasi, belum menyediakan akun tim terpisah atau pemulihan password otomatis.

### 3. Install dependency & jalankan dev server

```bash
npm install
npm run dev
```

Buka `http://localhost:3000`. Saat development, service worker dinonaktifkan otomatis (`next-pwa` di-disable saat `NODE_ENV=development`) supaya tidak mengganggu hot reload — matikan/uninstall service worker lama di DevTools kalau pernah mencoba build production sebelumnya di origin yang sama.

Endpoint Go (`api/gateway.go`) berjalan sebagai Vercel Function — untuk menjalankannya lokal persis seperti di production (termasuk `rewrites` di `vercel.json`), pakai Vercel CLI:

```bash
npm install -g vercel
vercel dev
```

## Build production

```bash
npm run build
npm run start
```

## Setup kilat: cukup variabel inti

Orang lain yang memasang DevControl hanya perlu mengisi variabel inti di Vercel → Settings → Environment Variables, lalu Redeploy:

| Variabel | Wajib | Fungsi |
|---|---|---|
| `CF_API_TOKEN` | ya | Akun, database D1 `devcontrol-db`, dan bucket R2 dideteksi atau dibuat otomatis |
| `DEVCONTROL_ADMIN_PASSWORD` | ya | Kata sandi admin (≥16 karakter); rahasia sesi & kunci arsip diturunkan darinya |
| `GITHUB_TOKEN` | fitur deploy | Aplikasi Baru, Update Aplikasi, Update Diri |
| `VERCEL_TOKEN` | fitur deploy | Build uji, deploy, penyimpanan `CF_ZONE_ID`; team dideteksi otomatis |

Aktifkan juga **Automatically expose System Environment Variables** agar project dikenali.

Alurnya setelah login admin (paket `pkg/autoconfig`, endpoint `/api/auto-setup`, komponen `ConfirmationCenter`):

1. Nilai yang bisa dihitung atau dicari langsung diisi saat runtime; nilai yang Anda isi manual selalu menang.
2. Langkah tanpa keputusan (buat D1, pasang tabel, buat bucket R2, aktifkan metrik API) dijalankan otomatis dengan panel progres.
3. Bila perlu keputusan — token punya beberapa akun Cloudflare, atau zona Cloudflare cocok dengan domain aplikasi — jendela konfirmasi muncul saat itu juga (status dipantau tiap 5 detik selama setup belum tuntas). Jika aplikasi sedang di latar belakang dan notifikasi diizinkan, konfirmasi juga dikirim sebagai notifikasi.
4. Sebelum login, halaman masuk menampilkan variabel inti mana yang masih kosong.

Instalasi lama yang semua variabelnya sudah terisi tidak berubah perilakunya.

## Tes otomatis

CI GitHub Actions (`.github/workflows/ci.yml`) menjalankan `go test ./...`, `go vet ./...`, `go build ./...`, dan `npm run build` pada setiap push ke `main`, termasuk hasil Update Diri; hasilnya ada di tab **Actions** repo GitHub. Selain itu, setiap Update Diri mengompilasi kode Go di tahap **Uji Build Vercel** sebelum apa pun didorong ke GitHub, jadi kesalahan kompilasi menghentikan update tanpa mengubah production.

Sejak v1.0.69 ada tes untuk:

- `pkg/webpush`: enkripsi notifikasi diuji dengan data uji resmi RFC 8291 (pesan terenkripsi harus bisa dibuka seperti oleh browser), tanda tangan VAPID ES256 diverifikasi, pasangan kunci yang tidak cocok ditolak, hanya alamat push resmi (Google, Mozilla, Apple, Microsoft) yang diterima, dan jenis notifikasi khusus admin tidak bisa dipilih member.
- `pkg/diagnose`: contoh error nyata (Deployment Protection, log build kosong, 404 Vercel, error kompilasi Go, paket npm hilang, token GitHub, gangguan layanan, environment variable kosong, dan error yang polanya belum dikenal) harus masuk ke sumber yang benar; letak error dan potongan kode dari ZIP diuji; diagnosis lama harus diklasifikasi ulang; dan setiap aturan wajib punya sumber serta saran perbaikan.

## Diagnosis error & retensi ZIP

Setiap kegagalan Aplikasi Baru, Update Aplikasi, dan Update Diri dianalisis otomatis (`pkg/diagnose`, tanpa layanan luar):

- **Letak error**: tahap pipeline, file, baris, dan kolom. Diambil dari log build Vercel (Go, TypeScript, ESLint, webpack/Next.js, npm), lalu dicocokkan dengan isi ZIP.
- **Potongan kode** di sekitar baris error, dibaca dari ZIP yang diunggah sebelum ZIP itu dibuang.
- **Sumber masalah**, ditampilkan sebagai badge paling atas sebelum detail lain: **kode di ZIP** (perbaiki di project aplikasi ini), **pengaturan project Vercel aplikasi ini** (Framework/Root Directory/Output Directory/Environment Variables — bukan kredensial DevControl), atau **DevControl sendiri** (token GITHUB_TOKEN/VERCEL_TOKEN/CF_API_TOKEN di Vercel milik DevControl, atau gangguan sementara GitHub/Vercel/Cloudflare). Untuk Update Diri, "kode" berarti kode DevControl sendiri, bukan aplikasi lain.
  Sejak v1.0.67 sumber ditentukan berlapis: (1) pola error yang dikenali; (2) bila polanya belum dikenali, tanda lain — file:baris yang ditemukan di ZIP atau perintah build proyek yang gagal di log Vercel berarti **kode di ZIP**, pesan DevControl tentang GitHub/Vercel/Cloudflare/D1/R2 berarti **DevControl sendiri**, lalu tahap tempat proses berhenti. "Belum bisa dipastikan" hanya muncul bila memang tidak ada bukti sama sekali. Diagnosis lama yang tersimpan sebelum ada klasifikasi sumber juga diklasifikasi ulang otomatis saat ditampilkan.
- **Log build yang belum tersedia dibaca ulang otomatis.** Bila Vercel belum menyimpan log saat build gagal, diagnosis menampilkan "Sumber sedang dipastikan" lalu DevControl membaca ulang log itu tiap 10 detik (±2 menit) selama diagnosis dibuka. Begitu terbaca, diagnosis diganti dengan error asli, sumber, dan letaknya, dan project uji Vercel yang tertinggal langsung dihapus.
- **Kategori, penyebab, dan saran perbaikan** konkret.
- **Tombol "Salin prompt untuk AI"**: berisi error, lokasi, potongan kode, log, dan instruksi perbaikan patch-only. Tinggal ditempel ke AI.

Diagnosis tampil di modal deployment dan di Pipeline ("Lihat letak error & saran perbaikan"). Pipeline yang gagal disimpan 24 jam, pipeline sukses 30 menit.

Retensi arsip ZIP (`pkg/archive`: `Discard`, `Finalize`, `Sweep`):

| Kejadian | Hasil |
|---|---|
| Aplikasi baru berhasil | ZIP disimpan sebagai satu-satunya versi |
| Aplikasi baru gagal | ZIP dibuang, tidak ada yang disimpan |
| Update aplikasi / update diri berhasil | ZIP baru disimpan, ZIP lama dihapus |
| Update aplikasi / update diri gagal | ZIP baru dibuang, ZIP lama tetap aktif (juga bila update diri gagal setelah push GitHub) |

Sisa arsip lama (status `previous` / `failed`) dibersihkan bertahap oleh pembersihan berkala.

## Member, role akses & keamanan

Menu **Member & Akses** (`/team`, khusus owner, `pkg/auth/members.go`):

| Role | Boleh |
|---|---|
| Owner | Kata sandi admin. Semua fitur + kelola member |
| Admin | Semua fitur kecuali kelola member |
| Operator | Lihat dashboard, Aplikasi Baru/Update Aplikasi, tutup proses gagal |
| Viewer | Hanya melihat |

- Member masuk dengan token `dcm_…` (256-bit acak). Server hanya menyimpan hash SHA-256-nya, dan token ditampilkan sekali saja.
- Opsional: kunci member ke IP/CIDR tertentu. Kunci ini dicek saat login dan di setiap request.
- Role, pencabutan akses, dan ganti token berlaku dalam ≤15 detik tanpa menunggu cookie kedaluwarsa (epoch per member). Tombol "Keluarkan semua sesi" me-logout semua orang, termasuk owner.
- Login: 5 kali gagal per IP → IP dikunci bertahap 1 → 60 menit, ditambah jeda 0,6 detik per kegagalan. Pesan error tidak membedakan kata sandi dan token. Semua login tercatat di audit log.
- Cookie sesi: `__Host-`, HttpOnly, Secure, SameSite=Strict, ditandatangani HMAC, berlaku 12 jam. Mutasi wajib same-origin.
- Header keamanan global di `vercel.json`: CSP, HSTS, X-Frame-Options DENY, nosniff, Referrer-Policy, Permissions-Policy, COOP/CORP.
- Unduh arsip ZIP butuh sesi owner/admin **dan** kunci arsip.

## Sinkron GitHub: file yang tidak dipakai lagi (`pkg/reposync`)

Setelah ZIP diekstrak, isinya dibandingkan dengan file di GitHub, satu per satu:

| File di GitHub yang tidak ada di ZIP | Tindakan |
|---|---|
| Kode/aset biasa | **Dihapus** (dianggap tidak dipakai lagi) |
| Hasil `npm install`/build yang ter-commit (`node_modules`, `.next`, `.turbo`, `__pycache__`, …) | **Dihapus** |
| Lockfile (`package-lock.json`, `yarn.lock`, `pnpm-lock.yaml`, `go.sum`, …) | **Dipertahankan**, kecuali ZIP membawa lockfile lain di folder yang sama |
| `.github/`, `.gitignore`, `.npmrc`, `.nvmrc`, `LICENSE`, `CODEOWNERS`, … | **Dipertahankan** |
| `.env` rahasia | **Dipertahankan** + peringatan |

Aturan tambahan:

- Dari ZIP, `__MACOSX`/`.DS_Store` dan hasil install/build tidak pernah ikut di-deploy. File `.env` rahasia tetap ikut di-deploy ke Vercel, tetapi tidak didorong ke GitHub.
- **Pengaman:** jika lebih dari separuh file sumber (dan lebih dari 10 file) akan terhapus, penghapusan dibatalkan. Ini biasanya tanda ZIP salah folder.
- **Waktu penghapusan:**
  - Aplikasi Baru/Update Aplikasi: file lama dihapus lewat commit terpisah **setelah aplikasi online**.
  - Update Diri: penghapusan dilakukan dalam commit yang sama setelah Uji Vercel lulus, karena production DevControl dibangun dari commit itu.
- Ringkasannya tampil di Riwayat update.

## Deploy: GitHub → Vercel

### Deployment aplikasi dari dashboard

Untuk database Cloudflare D1 yang **sudah ada**, login lalu jalankan penyiapan melalui menu **Databases**. Alternatif terminal tetap `npm run db:migrate` dan `npm run db:verify`; data yang ada tidak dihapus oleh skema tambahan.

### Arsip ZIP lintas perangkat (v1.0.8)

1. Isi `CF_R2_BUCKET` dengan nama bucket privat yang unik. Penyiapan akan membuat bucket bila belum ada; `CF_API_TOKEN` memerlukan izin **Workers R2 Storage Read** dan **Workers R2 Storage Write**. Simpan token hanya sebagai variabel server.
2. Set `ZIP_ARCHIVE_ACCESS_TOKEN` (kunci acak minimal 16 karakter) dan dua variabel admin di Vercel, lalu deploy versi ini. Jalankan penyiapan dari menu Databases sebelum memulai update; pada database lama kolom dan tabel yang diperlukan akan ditambahkan.
3. Setiap ZIP yang diterima pada awal Aplikasi Baru, Update Aplikasi, atau Update Diri disimpan persis bitnya di R2 sebelum proses lain berjalan. Riwayat ada di **Projects → Arsip ZIP aplikasi**; masukkan kunci untuk melihat dan mengunduh pada perangkat lain. Kunci disimpan sementara per tab browser. Jika penyimpanan gagal, aplikasi menolak update; ZIP lama tidak diganti. Jika build gagal, ZIP baru tetap ada dengan status gagal, sedangkan versi sebelumnya masih dapat diunduh.

Saat pertama kali memperbarui aplikasi versi lama yang belum punya arsip, DevControl lebih dulu mencoba menyimpan **snapshot repo GitHub** pada branch sebelumnya sebagai versi awal. Snapshot ini adalah sumber di GitHub saat itu, bukan salinan bit yang sama dengan ZIP historis atau bukti bahwa deploy production lama cocok persis. Jika repo tidak dapat dibaca atau snapshot melebihi 4 MiB, update dibatalkan dan ZIP baru tetap dicatat sebagai gagal. Update Diri ditandai berhasil setelah GitHub diperbarui dan check deployment production Vercel untuk commit tersebut berhasil. Arsip sebelumnya tersedia untuk pemulihan manual; fitur ini tidak melakukan rollback otomatis pada GitHub atau Vercel. Untuk fitur download, kedua jenis unggahan dibatasi **4 MiB** sesuai batas request/response Vercel Function. Akses arsip memerlukan koneksi online.

Set `GITHUB_TOKEN` dan `VERCEL_TOKEN` pada lingkungan DevControl; `VERCEL_TEAM_ID` diperlukan bila project ada dalam team. `CF_API_TOKEN` juga memerlukan izin **Workers Scripts Edit** agar DevControl dapat memasang runner terjadwal secara otomatis. Fitur **Aplikasi Baru** dan **Update Aplikasi** menyimpan ZIP di R2 sebelum menguji build di Vercel, mendorongnya ke GitHub, dan melakukan deploy production. Status tiap tahap terlihat di modal dan Pipeline. ZIP yang gagal diuji tidak didorong ke GitHub.

Status build `READY` diperiksa lagi dengan membuka halaman utama pada tautan yang akan disimpan. Alias juga dipastikan menunjuk ke ID deployment yang baru, sehingga halaman versi lama yang masih sehat tidak dihitung sebagai keberhasilan versi baru. Jika alias gagal, aplikasi mencoba alias lain dan URL deployment tetap; jika semuanya mengembalikan error HTTP, terhalang Deployment Protection, atau tidak bisa dihubungi, tahap production ditandai gagal dan tautan lama tidak diganti. Lihat alasan dan URL yang diuji di Pipeline. Sinkronisasi dan pembersihan file GitHub dilakukan dalam satu commit sebelum deployment production; tidak ada commit susulan setelah status berhasil yang bisa memicu build Vercel tanpa pemeriksaan.

Jika repo sudah di-import manual pada project Vercel bernama lain, DevControl mencari koneksi repo tersebut dan memilih project dengan **Root Directory aplikasi web yang sama**. Repo monorepo boleh mempunyai project lain untuk API, misalnya `server/`; project API tidak akan diubah menjadi frontend. Folder web dari ZIP (misalnya `web/`) dipakai konsisten pada build uji, pemeriksaan halaman utama, pengaturan project Git, dan pembacaan `.env` milik web. Bila ada beberapa web root yang sama kuat, alur berhenti sebelum push. Sebelum push, framework dan root yang berbeda dari build uji diselaraskan dengan project; override output/build/install lama dibersihkan hanya jika root atau framework berbeda. Pengaturan manual pada project yang sudah cocok dipertahankan. Variabel `.env` disalin sebelum project baru dihubungkan ke Git agar build otomatis pertama sudah memilikinya. Jika project untuk Update Aplikasi tidak ditemukan pada akun/team token, alur berhenti sebelum push dan meminta pemeriksaan `VERCEL_TEAM_ID`.

**Pemulihan aplikasi lama:** pada Projects → menu tiga titik milik aplikasi, **Perbaiki 404 Vercel** dan **Impor ulang web Vercel** hanya tampil bagi owner/admin jika URL production yang tersimpan benar-benar mengembalikan `404 NOT_FOUND` dari Vercel. Aplikasi sehat, 404 buatan aplikasi, kegagalan jaringan, dan domain yang belum dapat dibuktikan sebagai `vercel.app` tidak memunculkan aksi. Perbaikan biasa membaca repo dan branch GitHub, mendeteksi root web, mencari import Git yang sudah sehat, lalu mengadopsi tautannya. Jika belum ada, ia menyelaraskan project web terkait dan membangun ulang SHA commit yang sudah ada. Tombol ini tidak menghapus project lama. Import manual yang sehat tidak harus memakai SHA terbaru agar dapat diadopsi, tetapi halaman dan ID deployment tetap diverifikasi.

Jika perbaikan biasa masih menyisakan 404, pilih **Impor ulang web Vercel** dan ketik nama lengkap repo sebagai konfirmasi. DevControl mengidentifikasi project web lama dari URL atau nama stabilnya, memastikan root project bukan root API, memeriksa domain khusus, lalu membaca dan menyalin variabel Vercel tanpa menampilkan nilainya ke browser. Project web baru dibuat dari **repo dan SHA Git yang sudah ada** dengan Root Directory web dan preset framework yang benar. Setelah halaman pengganti benar-benar online dan terikat pada ID deployment baru, tautan aplikasi disimpan, lalu **hanya project web lama** dihapus. Kegagalan sebelum verifikasi mempertahankan project lama dan tautan lama; kegagalan saat pembersihan meninggalkan project lama tetapi tautan aplikasi yang sudah sehat tetap aktif. Jika project lama tidak bisa dibuktikan, variabel rahasia tidak bisa dibaca, atau terdapat domain khusus, tindakan berhenti tanpa menghapus project lama; pindahkan domain tersebut dengan aman di Vercel lebih dulu. Project API `server/` dan repo GitHub tidak dihapus. Bila project ada pada team yang tidak terlihat oleh `VERCEL_TOKEN`, atur `VERCEL_TEAM_ID` pada DevControl.

Contoh Apotik Pintar membutuhkan dua project dari repo yang sama: `server/` untuk API dan `web/` untuk Next.js. Kolom URL API opsional pada impor ulang memungkinkan mengganti `API_URL` project web dengan URL HTTPS project API server; kosongkan untuk mempertahankan `API_URL` lama. ZIP contoh hanya memiliki `.env.example` dengan `http://localhost:8080`, sehingga URL API production yang sebenarnya harus diisi bila variabel project web lama belum benar. Pengecekan keberhasilan di fitur ini memverifikasi halaman utama dan ID deployment, bukan setiap fungsi bisnis yang memakai API.

Pada project yang sudah terhubung ke GitHub, kegagalan memulai deployment commit dilaporkan sebagai gagal bersama SHA dan nama project untuk diperiksa di Vercel. Alur tidak menutupinya dengan deployment ZIP, karena build Git otomatis yang masih berjalan dapat mengganti alias production sesudah pengecekan ZIP selesai.

**Aplikasi Baru**, **Update Aplikasi**, dan **Update Diri** menyimpan ZIP sebelum melanjutkan tahap lain. Setelah konfirmasi **ZIP tersimpan**, laptop boleh ditutup: Cloudflare Worker terjadwal membaca pekerjaan di D1 setiap menit dan melanjutkan dari ZIP di R2. Browser hanya memantau hasil tiap 5 detik; setelah membuka aplikasi lagi, periksa Pipeline di perangkat mana pun. Repo yang sama dikunci hingga proses selesai. Jadwal Worker baru dapat memerlukan hingga 15 menit untuk aktif. Jika token tidak memiliki izin Workers Scripts Edit, permintaan ditolak sebelum ZIP diterima dan aplikasi menampilkan sebabnya. Jika respons dari tindakan GitHub atau Vercel tidak diketahui, Pipeline menandai proses **Terputus**; periksa layanan terkait sebelum mencoba lagi.

Pipeline menyimpan empat tahap untuk setiap ZIP secara terpisah di tabel `deployment_jobs` serta kursor pada `deployment_runner`. Proses yang masih berjalan tetap terlihat; proses berhasil, gagal, atau terputus otomatis dihapus dari D1 tiga puluh menit setelah status terakhir, saat halaman Pipeline diakses atau deployment berikutnya dimulai. Tiga proses terbaru terlihat di Overview. Migrasi membuat tabel dan indeks kunci repo pada database lama. Jika tabel atau koneksi D1 bermasalah, halaman menampilkan pesan galat dan tombol coba lagi. Untuk memasang perbaikan pada situs yang masih memakai backend lama, deploy ZIP ini ke repo DevControl melalui GitHub/Vercel sekali lalu muat ulang situs agar versi aplikasi dan service worker terbaru aktif.

Waktu mulai dan selesai tahap juga dapat dibaca dari data D1 lama yang menyimpan detik Unix sebagai angka desimal berakhiran `.0`; pembaruan tahap baru disimpan sebagai bilangan bulat. Dropdown lonceng membaca status pipeline yang sama dan hanya menampilkan proses yang masih ada dalam periode riwayat tiga puluh menit tersebut.

Untuk membuat deployment dari file ZIP lewat API, DevControl mengirim preset framework yang terdeteksi pada `projectSettings` dan mengonfirmasi pengaturan yang diminta Vercel. Deployment dari commit Git memakai pengaturan build project yang sudah diselaraskan dan tetap mengaktifkan pemeriksaan deteksi framework Vercel; perbedaan setelan tidak lagi disembunyikan oleh `skipAutoDetectionConfirmation=1`.

Jika **Update Diri** dari versi lama gagal dengan pesan `projectSettings object is required`, kegagalan terjadi sebelum push ke GitHub. Versi yang memperbaiki alur tersebut perlu dipasang sekali melalui GitHub/Vercel secara manual pada proyek DevControl yang sudah ada. Setelah versi baru online, ulangi Update Diri dari aplikasi. File ZIP ini berisi seluruh sumber aplikasi; jangan gunakan fitur Update Diri yang masih berjalan pada versi lama untuk memasang perbaikannya.

Hal yang sama berlaku jika versi yang sedang online masih menampilkan `Build gagal (status: TIMEOUT)`: unggah ZIP versi terbaru ini ke repo DevControl dan deploy lewat GitHub/Vercel sekali agar backend pemeriksa status baru aktif, lalu gunakan Update Diri atau Update Aplikasi dari versi yang sudah diperbarui.

### Repository GitHub di halaman Projects

Halaman **Projects** membaca `/api/github-repos` sebagai sumber daftar repo dari akun/token GitHub yang dikonfigurasi. Repo yang sudah ada di GitHub kini muncul walaupun belum pernah dideploy melalui DevControl. Jika sebuah repo juga tercatat di tabel D1 `services`, kartu tersebut menampilkan URL aplikasi yang disimpan setelah deployment. Repo yang belum pernah dihubungkan diberi keterangan "Belum terhubung ke deployment"; keberadaan repo GitHub sendiri tidak membuktikan aplikasi sudah online di Vercel. Daftar **Update Aplikasi** tetap berisi aplikasi yang telah tercatat di D1 agar tidak menimpa repo GitHub lain secara tidak sengaja.

Banner pada halaman **Projects** diatur dari **Settings → Banner halaman Aplikasi**. Sejak v1.0.59, JPG/PNG/WebP diunggah apa adanya (tanpa kompresi, crop, atau pengecilan resolusi; batas hanya 5 GiB dari R2), jadi gambar 4K tetap tersimpan dan dikirim ke browser sebagai 4K. Banner ditampilkan dalam bingkai rasio 1440 × 450 (16:5); rasio lain hanya dipotong tengah pada tampilan, berkas aslinya tetap utuh. Pratinjau menunjukkan resolusi dan ukuran berkas asli. Nama aplikasi dan tautannya diketik manual; keduanya, beserta judul dan deskripsi, opsional sehingga gambar bisa menjadi banner umum. Teks dan tombol, bila diisi, tampil di area terpisah di bawah gambar sehingga gambar tidak tertutup lapisan gelap. Metadata disimpan di D1 dan gambar dikirim langsung ke R2 privat agar muncul pada perangkat lain setelah pemeriksaan data berikutnya. Konfigurasi R2 S3/CORS yang sama seperti thumbnail tetap diperlukan. Pengolahan banner ini tidak mengubah thumbnail kartu.

Setiap kartu memiliki area gambar 4:3 dengan ilustrasi bawaan. Menu tiga titik di kanan atas gambar berisi **Gambar aplikasi**, **Arsip ZIP**, **Riwayat update**, dan **Hapus aplikasi** yang tetap meminta konfirmasi. Sejak v1.0.62 dialog **Gambar aplikasi** menyimpan enam gambar opsional per aplikasi: thumbnail mode terang, thumbnail mode gelap, logo mode terang, logo mode gelap, desain mode terang, dan desain mode gelap. Kartu memakai thumbnail sesuai mode tampilan yang aktif; bila hanya satu thumbnail yang diisi, thumbnail itu dipakai di kedua mode (thumbnail lama otomatis menjadi thumbnail mode terang). Kedua logo dan kedua desain tidak tampil di kartu, tetapi bisa dibuka dari dialog dalam ukuran asli, pas layar, atau di tab baru. Semua role yang login dapat melihat; hanya owner/admin yang dapat mengunggah, mengganti, dan menghapus. Gambar tambahan disimpan di slot `owner/repo~dark`, `~logo-light`, `~logo-dark`, `~design-light`, dan `~design-dark` pada tabel dan bucket yang sama, dan ikut terhapus saat aplikasi dihapus. JPG/PNG/WebP asli diunggah langsung dari browser ke bucket R2 privat: gambar tidak dikompresi atau diubah resolusinya oleh aplikasi, sementara kartu menampilkannya dengan pemotongan visual 4:3. Tidak ada batas MB buatan aplikasi; R2 membatasi satu unggahan langsung hingga 5 GiB. Daftar Projects memeriksa repo tiap 15 detik serta layanan dan thumbnail tiap 10 detik ketika tab terlihat; perubahan di tab lain pada perangkat yang sama dikirim langsung dan tab yang kembali aktif langsung menyegarkan data. Perangkat lain menerima perubahan pada pemeriksaan berikutnya tanpa perlu memuat ulang halaman.

Unggahan gambar besar kini dapat memakai `CF_API_TOKEN` yang sudah terpasang, bila token itu memiliki izin R2 Object Read & Write untuk bucket ini. Server memeriksa ID token dan menurunkan kredensial S3 di memori; nilai rahasianya tidak dikirim ke browser. Jika token tersebut tidak memiliki izin R2 yang sesuai, buat **R2 S3 API token** khusus bucket dengan hak baca/tulis, isi `R2_ACCESS_KEY_ID` dan `R2_SECRET_ACCESS_KEY` di Environment Variables Vercel, lalu deploy ulang. Kedua variabel ini opsional jika token awal dapat dipakai.

Saat unggah dimulai, aplikasi mencoba menambahkan aturan CORS `PUT` untuk domain DevControl pada bucket R2 tanpa menghapus aturan yang sudah ada. Jika token Cloudflare tidak boleh mengatur CORS bucket, atur manual melalui Cloudflare R2 → bucket → Settings → CORS Policy (ganti domain sesuai situs Anda):

```json
[
  {
    "AllowedOrigins": ["https://devcontrol-anda.vercel.app"],
    "AllowedMethods": ["PUT"],
    "AllowedHeaders": ["Content-Type"],
    "MaxAgeSeconds": 3600
  }
]
```

Izin memulai unggahan berlaku untuk satu objek selama 15 menit. Setelah browser mengirim byte asli ke R2, backend memeriksa ukuran, tipe, dan tanda awal berkas sebelum mengaktifkannya di kartu; objek yang tidak selesai disiapkan untuk dibersihkan. Gambar v1.0.17 tetap bisa dibuka tanpa konfigurasi baru. Di tablet (lebar 768–1279 px) susunan kolom mengikuti desktop dengan ukuran huruf dan jarak yang lebih ringkas.

### Notifikasi push (v1.0.61)

Notifikasi tetap masuk walaupun DevControl ditutup, memakai standar **Web Push** browser (tanpa Firebase atau layanan berbayar). Tidak ada variabel Vercel baru: pasangan kunci VAPID dibuat otomatis saat pertama dipakai dan disimpan di D1 (tabel `push_config`, `push_subscriptions`, `push_log` ditambahkan otomatis). Bila ingin memakai kunci sendiri, isi `VAPID_PUBLIC_KEY` dan `VAPID_PRIVATE_KEY` (base64url, P-256) di Vercel.

Sejak v1.0.63, DevControl meminta izin notifikasi secara otomatis begitu dibuka, selama izinnya belum pernah dijawab di perangkat itu; tidak perlu membuka Settings dulu. Ini hanya berjalan di Chrome, Edge, dan Firefox — Safari (desktop maupun PWA terpasang di iOS/iPadOS) mengharuskan permintaan izin berasal dari ketukan pengguna, jadi di Safari permintaan otomatisnya diam-diam tidak melakukan apa pun dan tombol manual di bawah ini tetap dipakai. Kelola pilihan atau aktifkan manual lewat **menu profil → Notifikasi perangkat** (semua role) atau **Settings → Notifikasi perangkat ini** (admin). Jenis yang bisa dipilih: **Deploy aplikasi** (Aplikasi Baru/Update Aplikasi berhasil, gagal, atau terhenti), **Update diri**, **Konfirmasi menunggu** (khusus owner/admin), dan **Aplikasi 404**. Tombol **Kirim notifikasi uji** memeriksa jalurnya. Mengetuk notifikasi membuka halaman terkait. Keluar dari akun mematikan notifikasi di perangkat itu; member yang dicabut tidak lagi menerima apa pun.

Notifikasi deploy dan update diri dikirim langsung dari backend saat pipeline selesai, gagal, atau kedaluwarsa (satu kali per kejadian). Pemeriksaan 404 aplikasi dan konfirmasi zona Cloudflare dijalankan tiap 15 menit oleh Worker Cloudflare terjadwal yang sama dengan runner deployment; Worker versi baru dipasang otomatis ketika perangkat pertama mengaktifkan notifikasi atau saat deployment berikutnya. Sejak v1.0.66 ping 15 menit ini selalu berjalan (tidak lagi hanya bila ada perangkat berlangganan notifikasi), karena ping yang sama juga membersihkan project uji Vercel yang tertinggal — lihat "Uji Build Vercel gagal terbaca & project uji tersisa" di bawah.

Nama project uji ini juga sempat selalu tertulis `-selfupdate-test-`, meski untuk "Aplikasi Baru"/"Update Aplikasi" biasa, bukan cuma Update Diri; sejak v1.0.66 jadi `-test-` yang netral.

### Aplikasi Baru yang gagal tidak meninggalkan sisa (v1.0.70)

Aplikasi Baru membuat repo GitHub dan project Vercel aslinya di tahap **Dorong ke GitHub**, sebelum build production di tahap **Onlinekan di Vercel** terbukti berhasil. Sebelumnya, bila tahap terakhir itu gagal, repo dan project Vercel yang sudah dibuat dibiarkan, sehingga di Vercel muncul project gagal yang tidak pernah tercatat di DevControl.

Sejak v1.0.70, bila Aplikasi Baru gagal sebelum pernah online, DevControl menghapus **hanya yang dibuat oleh proses itu sendiri**: project Vercel-nya lalu repo GitHub-nya. Repo atau project yang sudah ada sebelum proses dimulai (misalnya hasil import manual di Vercel, atau repo dengan nama sama) tidak pernah disentuh, dan tidak ada yang dihapus setelah aplikasi tersimpan sebagai online. Update Aplikasi tidak pernah menghapus apa pun. Bila log build production belum terbaca, penghapusan project Vercel ditunda 30 menit agar log masih bisa dibaca ulang. Bila GITHUB_TOKEN tidak punya izin hapus repo (Administration: write), repo dibiarkan dan pesan pipeline menyebutkannya; hapus lewat Projects → menu kartu → Hapus aplikasi. Pesan pipeline selalu merinci apa yang dibersihkan.

Sisa dari percobaan sebelum v1.0.70 tidak ikut dibersihkan otomatis; hapus lewat Projects → Hapus aplikasi, atau langsung di Vercel/GitHub.

### Deployment Protection pada project uji (v1.0.68)

Setelah build uji READY, DevControl membuka halaman utama project uji untuk memastikan aplikasi benar-benar tersaji. Banyak team Vercel menerapkan Deployment Protection (Vercel Authentication) ke semua project baru, sehingga pemeriksaan itu dialihkan ke login Vercel (HTTP 302/401/403) walaupun kode aplikasinya benar. Sejak v1.0.68 DevControl melepas proteksi project uji-nya sendiri, membuat kunci **Protection Bypass for Automation** untuk project uji itu dan mengirimnya sebagai header `x-vercel-protection-bypass` saat memeriksa, serta menunggu hingga ±10 detik agar perubahan proteksi sampai ke edge Vercel. Proteksi project production aplikasi tidak diubah. Bila tetap gagal, pesan menyebutkan alasannya (misalnya VERCEL_TOKEN tidak punya akses ke team), diagnosis menandainya sebagai **DevControl sendiri**, dan project uji dibersihkan otomatis setelah 30 menit.

### Uji Build Vercel gagal terbaca & project uji tersisa (v1.0.66)

Saat build uji Vercel gagal, DevControl membaca log build itu sebelum menghapus project uji sementara (`<nama>-selfupdate-test-<waktu>`), supaya diagnosis dan potongan errornya tetap ada. Sesekali endpoint log Vercel belum sempat menyimpan hasilnya tepat saat status berubah jadi ERROR, sehingga muncul pesan "Log belum dapat dibaca (log build kosong)" padahal buildnya betulan gagal — dan karena project uji sengaja tidak dihapus ketika lognya gagal dibaca (supaya tidak menghilangkan buktinya), project itu tertinggal di Vercel.

Sejak v1.0.66: pembacaan log dicoba ulang otomatis (sampai 3 kali dalam ~7 detik) sebelum menyerah, sehingga kejadian ini jauh lebih jarang. Kalau log tetap tidak terbaca setelah dicoba ulang, project ujinya dicatat di tabel `vercel_orphan_projects` dan dihapus otomatis 30 menit kemudian oleh pembersihan berkala — cukup waktu untuk membukanya manual di Vercel bila perlu, tanpa menumpuk selamanya.

Sejak v1.0.64 notifikasi memakai service worker tersendiri, `public/push-sw.js` (scope `/push/`), terpisah dari service worker PWA `/sw.js` yang dibuat `next-pwa`. Keduanya tidak saling bergantung: masalah pada salah satunya tidak memblokir yang lain, dan push tidak lagi bergantung pada proses build `next-pwa`.

Batasan platform: Android (Chrome/Edge/Firefox) tetap menerima walau browser ditutup; iPhone/iPad memerlukan iOS/iPadOS 16.4+ dan DevControl dipasang ke Layar Utama lewat Safari lalu diaktifkan dari ikon tersebut; di laptop/PC browser harus tetap berjalan di latar belakang (tab boleh ditutup).

### Logo aplikasi di Pengaturan

Di **Settings → Logo aplikasi**, pilih latar **Transparan**, **Hitam**, atau **Putih**, lalu pilih PNG/JPG/WebP dan klik **Simpan logo**. Sejak v1.0.59 file logo asli disimpan utuh di R2 privat (slot `__devcontrol__/logo-<versi>`, terikat ke versi logo) dan dipakai di sidebar serta Pengaturan tanpa kompresi atau pengecilan resolusi. Ikon 192/512/maskable tetap dibuat karena ukuran itu diwajibkan browser untuk favicon dan PWA; pilihan latar hanya berlaku untuk ikon tersebut. Unggah logo asli memakai jalur R2 S3/CORS yang sama dengan thumbnail; jika belum siap, ikon tetap tersimpan dan sidebar sementara memakai ikon 512 px. Logo yang disimpan sebelum v1.0.59 belum punya file asli, jadi simpan ulang untuk memakai resolusi penuh. Pilihan transparan menyimpan alfa PNG aslinya pada ikon PNG persegi ukuran 192 dan 512 serta varian maskable; untuk hasil benar-benar bening gunakan sumber PNG transparan. Logo lama dengan latar hitam yang sudah menyatu ke ikon memerlukan unggah ulang sumber aslinya. Gambar sumber hanya diproses di perangkat dan ikon hasilnya disimpan di bucket R2 privat. Pastikan `CF_R2_BUCKET` dan `CF_API_TOKEN` dengan izin R2 Read/Write sudah dikonfigurasi. Tabel `app_branding` ditambahkan otomatis saat halaman Pengaturan diakses oleh admin, atau melalui **Databases → Siapkan D1 + R2**. Ikon hasil (bukan logo asli) dikirim ke backend dengan batas request 4 MiB.

Perubahan logo ditampilkan pada sidebar, favicon, dan ikon PWA melalui manifest `/manifest.json` yang disajikan dinamis. Tab terbuka pada perangkat lain memeriksa versi logo setiap 30 detik. Ikon untuk pemasangan baru langsung memakai logo terakhir; ikon layar utama yang **sudah dipasang di iOS/iPadOS** dikelola sistem dan tidak bisa diperbarui oleh aplikasi web. Hapus pintasan lama, buka situs di Safari, lalu **Bagikan → Tambahkan ke Layar Utama** untuk memasang ikon baru. Browser desktop juga dapat mempertahankan ikon lama sehingga mungkin perlu pemasangan ulang; Chrome pada Android dapat memperbarui WebAPK kemudian. Tombol **Kembalikan bawaan** mengaktifkan lagi ikon asal.

Mode **Terang/Gelap** tersedia di dropdown profil dan disimpan di perangkat ini, termasuk saat aplikasi PWA dibuka tanpa koneksi. Mode terang mengubah warna latar, kartu, teks, garis, dan status mengikuti referensi tanpa mengubah susunan halaman. Pada aplikasi iPad yang terpasang, navbar menambah ruang untuk safe area jika perangkat memerlukannya; warna status bar mengikuti mode yang aktif jika didukung sistem.


Tombol **Hapus aplikasi** pada setiap kartu meminta pengetikan nama lengkap `owner/repo`. Penghapusan menghapus project Vercel yang terhubung ke repo pada akun/team yang dikonfigurasi, project Vercel bernama stabil buatan DevControl, repo GitHub, arsip ZIP aplikasi dan update diri terkait beserta thumbnail di R2, serta metadata services, thumbnail, API terkelola, dan pipeline di D1. Project Vercel juga menghapus deployment, domain, variabel lingkungan, dan pengaturannya. Jika salah satu provider gagal, dialog menampilkan tahap yang gagal dan tombol dapat dicoba lagi; arsip dan metadata yang belum selesai tetap tersedia untuk pengulangan. Repo yang sedang dideploy atau project Vercel DevControl aktif ditolak. Token GitHub harus memiliki izin menghapus repo (classic `delete_repo` atau fine-grained Administration write); `VERCEL_TOKEN` harus dapat menghapus project pada `VERCEL_TEAM_ID` yang sesuai. Project di akun/team Vercel lain tidak dapat ditemukan oleh token ini.

Penghapusan thumbnail memakai API S3 R2 yang juga dipakai untuk mengunggah gambar besar. Jika API S3 tidak tersedia, API Cloudflare dicoba sebagai cadangan; kesalahan dari Cloudflare ditampilkan agar penghapusan dapat dicoba lagi setelah izin bucket diperbaiki. Metadata thumbnail di D1 baru dihapus setelah objek R2 berhasil dihapus. Jika repo GitHub telah terhapus saat muncul kesalahan, gunakan tombol penghapusan pada kartu yang masih tersimpan untuk melanjutkan pembersihan.

**Environment Status** di Overview dan halaman Environments membaca deployment terkini dari API Vercel untuk aplikasi yang tercatat di `services.repo` (dan DevControl sendiri bila `VERCEL_PROJECT_ID` tersedia). Setiap baris menunjukkan lingkungan yang benar-benar ditemukan, nama aplikasi, revisi commit atau ID deployment, serta keadaan build yang dilaporkan Vercel. Tabel `environments` di D1 dan data contohnya tidak lagi ditampilkan; nama Staging/Development hanya muncul jika ada deployment nyata untuk lingkungan itu. Kolom Region lama diganti Project karena endpoint daftar deployment Vercel tidak menjamin region. Status Ready berarti build siap menurut Vercel, bukan bukti situs merespons atau metrik uptime. `VERCEL_TOKEN` harus dapat membaca deployment pada akun/team yang sama; saat gagal, antarmuka menandai data cache sebagai belum terverifikasi dan tidak menampilkan data contoh.

Jika kartu repo kosong, buka `/api/github-repos` pada domain DevControl yang aktif. Respons `412` berarti `GITHUB_TOKEN` belum terpasang; respons berisi error GitHub berarti periksa token dan hak akses; respons `[]` berarti token berfungsi tetapi belum melihat repo tersebut (misalnya token fine-grained hanya diberi akses ke repo tertentu atau pemilik repo yang berbeda). Periksa `GITHUB_TOKEN` pada environment Vercel yang sedang dipakai dan lakukan redeploy setelah mengubahnya. Halaman Projects dan modal Update Diri kini menampilkan kesalahan dari API secara jelas. Jangan pernah memasukkan token GitHub ke URL atau membagikannya di tangkapan layar.

Project production memakai commit GitHub jika integrasi Vercel tersedia, dengan unggahan ZIP sebagai cadangan. Untuk aplikasi yang membutuhkan variabel lingkungan saat build atau runtime, atur variabel tersebut pada project Vercel target. Build yang berhasil tidak menjamin fungsi aplikasi dengan layanan eksternal telah dikonfigurasi.

Batas unggahan ZIP pada alur ini 4 MiB agar permintaan tetap di bawah batas ukuran body Vercel Function.

1. **Push ke GitHub**
   ```bash
   git init
   git add .
   git commit -m "DevControl: offline-first PWA dashboard"
   git branch -M main
   git remote add origin https://github.com/<username>/devcontrol.git
   git push -u origin main
   ```

2. **Hubungkan ke Vercel**
   - Buka [vercel.com/new](https://vercel.com/new), pilih repo GitHub ini.
   - Vercel otomatis mendeteksi Next.js untuk frontend dan `go.mod` + `api/gateway.go` untuk backend Go — tidak perlu konfigurasi build tambahan.
   - Di tab **Environment Variables**, tambahkan `CF_ACCOUNT_ID`, `CF_D1_DATABASE_ID`, `CF_API_TOKEN`, `CF_R2_BUCKET`, `ZIP_ARCHIVE_ACCESS_TOKEN`, `DEVCONTROL_ADMIN_PASSWORD`, dan `DEVCONTROL_SESSION_SECRET` sesuai `.env.example`.
   - Klik **Deploy**.

3. Setiap `git push` ke `main` setelah ini otomatis di-deploy ulang oleh Vercel (continuous deployment bawaan integrasi GitHub).

## Catatan implementasi

- **Kenapa Go sebagai Vercel Functions, bukan server terpisah?** Ini menyatukan frontend dan backend dalam satu repo/satu deploy ke Vercel sesuai permintaan, tanpa perlu hosting Go terpisah.
- **Kenapa satu file `api/gateway.go` untuk semua endpoint, bukan satu file per endpoint?** Go mewajibkan satu folder = satu package, dan builder Go milik Vercel meng-compile seluruh isi folder `api/` sekaligus — jadi beberapa file yang masing-masing punya fungsi `Handler` sendiri (bahkan di subfolder terpisah) tetap bisa berujung "Handler redeclared". Dengan hanya **satu** fungsi `Handler` di seluruh proyek, konflik ini tidak mungkin terjadi lagi. Endpoint publik (`/api/overview`, `/api/services`, dst) tetap sama karena `vercel.json` me-rewrite tiap path ke `/api/gateway?resource=<nama>`.
- **Kenapa D1 diakses lewat REST API, bukan driver SQLite langsung?** Vercel Functions berjalan di lingkungan serverless yang tidak punya akses jaringan ke storage internal Cloudflare; REST API resmi Cloudflare adalah cara yang didukung untuk mengakses D1 dari luar Workers/Pages.
- **Data ditampilkan dulu dari IndexedDB (jika ada), lalu disegarkan dari jaringan** — supaya UI langsung terisi tanpa layar kosong saat baru dibuka, baik online maupun offline.
- Ikon PWA di `public/icons/` adalah pilihan bawaan; logo dapat diganti dari halaman Settings.
- Semua breakpoint memakai skala Tailwind standar (`sm`, `md`, `lg`, `xl`): sidebar berubah jadi drawer di bawah `lg`, grid statistik menyesuaikan dari 1 → 2 → 4 kolom, dan tabel bisa di-scroll horizontal di layar sempit.
