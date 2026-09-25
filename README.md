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

Untuk **Network** (byte respons HTTP dalam MiB) dan **Requests** (jumlah request), isi `CF_ZONE_ID` pada environment Vercel dan berikan `CF_API_TOKEN` izin **Account Analytics Read** atas akun/zona terkait, lalu deploy ulang. Zone ID ada di halaman Overview domain Cloudflare; pastikan trafik domain itu benar-benar melalui proxy Cloudflare. Data GraphQL dijumlah selama 24 jam terakhir untuk **semua hostname** dalam zona, dengan grafik per jam dan pembaruan paling cepat tiap menit. Cloudflare dapat memakai sampling adaptif sehingga total trafiknya berupa estimasi. Jika zona, izin, atau layanan analitik tidak tersedia, kartu menampilkan tanda `—` beserta petunjuk konfigurasi; tabel D1 `infra_metrics` dan data contoh tidak digunakan sebagai metrik langsung.

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

## Deploy: GitHub → Vercel

### Deployment aplikasi dari dashboard

Untuk database Cloudflare D1 yang **sudah ada**, login lalu jalankan penyiapan melalui menu **Databases**. Alternatif terminal tetap `npm run db:migrate` dan `npm run db:verify`; data yang ada tidak dihapus oleh skema tambahan.

### Arsip ZIP lintas perangkat (v1.0.8)

1. Isi `CF_R2_BUCKET` dengan nama bucket privat yang unik. Penyiapan akan membuat bucket bila belum ada; `CF_API_TOKEN` memerlukan izin **Workers R2 Storage Read** dan **Workers R2 Storage Write**. Simpan token hanya sebagai variabel server.
2. Set `ZIP_ARCHIVE_ACCESS_TOKEN` (kunci acak minimal 16 karakter) dan dua variabel admin di Vercel, lalu deploy versi ini. Jalankan penyiapan dari menu Databases sebelum memulai update; pada database lama kolom dan tabel yang diperlukan akan ditambahkan.
3. Setiap ZIP yang diterima pada awal Aplikasi Baru, Update Aplikasi, atau Update Diri disimpan persis bitnya di R2 sebelum proses lain berjalan. Riwayat ada di **Projects → Arsip ZIP aplikasi**; masukkan kunci untuk melihat dan mengunduh pada perangkat lain. Kunci disimpan sementara per tab browser. Jika penyimpanan gagal, aplikasi menolak update; ZIP lama tidak diganti. Jika build gagal, ZIP baru tetap ada dengan status gagal, sedangkan versi sebelumnya masih dapat diunduh.

Saat pertama kali memperbarui aplikasi versi lama yang belum punya arsip, DevControl lebih dulu mencoba menyimpan **snapshot repo GitHub** pada branch sebelumnya sebagai versi awal. Snapshot ini adalah sumber di GitHub saat itu, bukan salinan bit yang sama dengan ZIP historis atau bukti bahwa deploy production lama cocok persis. Jika repo tidak dapat dibaca atau snapshot melebihi 4 MiB, update dibatalkan dan ZIP baru tetap dicatat sebagai gagal. Update Diri ditandai berhasil setelah GitHub berhasil diperbarui; deployment production dari integrasi GitHub berlangsung secara terpisah. Arsip sebelumnya tersedia untuk pemulihan manual; fitur ini tidak melakukan rollback otomatis pada GitHub atau Vercel. Untuk fitur download, kedua jenis unggahan dibatasi **4 MiB** sesuai batas request/response Vercel Function. Akses arsip memerlukan koneksi online.

Set `GITHUB_TOKEN` dan `VERCEL_TOKEN` pada lingkungan DevControl; `VERCEL_TEAM_ID` diperlukan bila project ada dalam team. Fitur **Aplikasi Baru** dan **Update Aplikasi** mengirim ZIP melalui empat tahap: ekstrak, uji build pada project Vercel sementara, push ke GitHub, lalu deploy ke project Vercel production yang tetap. Status tiap tahap terlihat di modal dan pipeline. URL deployment production disimpan pada aplikasi dan digunakan oleh tombol **Buka aplikasi** pada Projects. ZIP yang gagal diuji tidak didorong ke GitHub; kegagalan pada tahap terakhir dilaporkan dan tidak dianggap selesai.

**Aplikasi Baru**, **Update Aplikasi**, dan **Update Diri** menampilkan tombol **Hide** selama proses berlangsung. Dialog disembunyikan tanpa menghentikan unggahan atau pemeriksaan build; panel kecil menampilkan setiap proses dan dapat membuka kembali dialog mana pun, membuka proses baru, atau membawa pengguna ke halaman **Deployments**. Beberapa proses dengan repo berbeda dapat berjalan sekaligus di satu tab. Repo yang sama dikunci sampai proses selesai agar push dan deployment production tidak saling bertabrakan, termasuk bila branch berbeda. Dialog tetap hidup saat berpindah halaman di tab yang sama, dan Pipeline menyegarkan status setiap 5 detik. Biarkan **tab tetap terbuka** sampai semua proses selesai; menutup atau memuat ulang tab menghentikan kelanjutan tahap yang dijalankan browser. Jika proses tidak dilanjutkan selama 10 menit, Pipeline menandainya **Terputus** dan kuncinya dilepas; periksa hasil di GitHub/Vercel sebelum mengunggah ulang. Permintaan pemeriksaan build dilakukan tiap 5 detik; GitHub hanya diperbarui setelah Vercel mengembalikan `READY`. Jika pemeriksaan status terputus berulang kali, periksa deployment di dashboard Vercel sebelum mengunggah ulang. Project uji sementara dihapus saat build gagal atau setelah tahap GitHub selesai; bila tab ditutup lebih awal, project uji mungkin perlu dihapus dari Vercel secara manual.

Pipeline menyimpan empat tahap untuk setiap ZIP secara terpisah di tabel `deployment_jobs`. Proses yang masih berjalan tetap terlihat; proses berhasil, gagal, atau terputus otomatis dihapus dari D1 lima menit setelah status terakhir, saat halaman Pipeline diakses atau deployment berikutnya dimulai. Tiga proses terbaru terlihat di Overview. Migrasi membuat tabel dan indeks kunci repo pada database lama. Jika tabel atau koneksi D1 bermasalah, halaman menampilkan pesan galat dan tombol coba lagi. Untuk memasang perbaikan pada situs yang masih memakai backend lama, deploy ZIP ini ke repo DevControl melalui GitHub/Vercel sekali lalu muat ulang situs agar versi aplikasi dan service worker terbaru aktif.

Untuk membuat proyek Vercel baru lewat API, ZIP dengan `package.json` Next.js mengirim `projectSettings.framework = "nextjs"`. ZIP framework lain memakai `skipAutoDetectionConfirmation=1` agar Vercel melakukan deteksi tanpa meminta konfirmasi interaktif. Pengaturan ini dipakai untuk project uji sementara dan project production yang dibuat lewat alur deployment.

Jika **Update Diri** dari versi lama gagal dengan pesan `projectSettings object is required`, kegagalan terjadi sebelum push ke GitHub. Versi yang memperbaiki alur tersebut perlu dipasang sekali melalui GitHub/Vercel secara manual pada proyek DevControl yang sudah ada. Setelah versi baru online, ulangi Update Diri dari aplikasi. File ZIP ini berisi seluruh sumber aplikasi; jangan gunakan fitur Update Diri yang masih berjalan pada versi lama untuk memasang perbaikannya.

Hal yang sama berlaku jika versi yang sedang online masih menampilkan `Build gagal (status: TIMEOUT)`: unggah ZIP versi terbaru ini ke repo DevControl dan deploy lewat GitHub/Vercel sekali agar backend pemeriksa status baru aktif, lalu gunakan Update Diri atau Update Aplikasi dari versi yang sudah diperbarui.

### Repository GitHub di halaman Projects

Halaman **Projects** membaca `/api/github-repos` sebagai sumber daftar repo dari akun/token GitHub yang dikonfigurasi. Repo yang sudah ada di GitHub kini muncul walaupun belum pernah dideploy melalui DevControl. Jika sebuah repo juga tercatat di tabel D1 `services`, kartu tersebut menampilkan URL aplikasi yang disimpan setelah deployment. Repo yang belum pernah dihubungkan diberi keterangan "Belum terhubung ke deployment"; keberadaan repo GitHub sendiri tidak membuktikan aplikasi sudah online di Vercel. Daftar **Update Aplikasi** tetap berisi aplikasi yang telah tercatat di D1 agar tidak menimpa repo GitHub lain secara tidak sengaja.

Setiap kartu memiliki area gambar 4:3 dengan ilustrasi bawaan. Pilih **Tambah thumbnail** untuk mengunggah JPG/PNG asli langsung dari browser ke bucket R2 privat: gambar tidak dikompresi atau diubah resolusinya oleh aplikasi, sementara kartu menampilkannya dengan pemotongan visual 4:3. Tidak ada batas MB buatan aplikasi; R2 membatasi satu unggahan langsung hingga 5 GiB. **Ganti thumbnail** dan **Hapus gambar** tersedia pada kartu yang sudah bergambar. Daftar Projects memeriksa repo tiap 15 detik serta layanan dan thumbnail tiap 10 detik ketika tab terlihat; perubahan di tab lain pada perangkat yang sama dikirim langsung dan tab yang kembali aktif langsung menyegarkan data. Perangkat lain menerima perubahan pada pemeriksaan berikutnya tanpa perlu memuat ulang halaman.

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

### Logo aplikasi di Pengaturan

Di **Settings → Logo aplikasi**, pilih PNG/JPG lalu klik **Simpan logo**. Browser membuat ikon PNG persegi ukuran 192 dan 512 serta varian maskable; gambar sumber hanya diproses di perangkat dan ikon hasilnya disimpan di bucket R2 privat. Pastikan `CF_R2_BUCKET` dan `CF_API_TOKEN` dengan izin R2 Read/Write sudah dikonfigurasi. Tabel `app_branding` ditambahkan otomatis saat halaman Pengaturan diakses oleh admin, atau melalui **Databases → Siapkan D1 + R2**. Tidak perlu konfigurasi S3/CORS untuk unggah logo karena ikon yang sudah diperkecil dikirim ke backend; batas request ikon hasil 4 MiB.

Perubahan logo ditampilkan pada sidebar, favicon, dan ikon PWA melalui manifest `/manifest.json` yang disajikan dinamis. Tab terbuka pada perangkat lain memeriksa versi logo setiap 30 detik. Ikon untuk pemasangan baru langsung memakai logo terakhir; ikon layar utama yang **sudah dipasang di iOS/iPadOS** dikelola sistem dan tidak bisa diperbarui oleh aplikasi web. Hapus pintasan lama, buka situs di Safari, lalu **Bagikan → Tambahkan ke Layar Utama** untuk memasang ikon baru. Browser desktop juga dapat mempertahankan ikon lama sehingga mungkin perlu pemasangan ulang; Chrome pada Android dapat memperbarui WebAPK kemudian. Tombol **Kembalikan bawaan** mengaktifkan lagi ikon asal.

Pada aplikasi iPad yang terpasang, status bar menggunakan mode gelap yang menempatkan konten di bawah jam dan baterai; navbar menambah ruang untuk safe area jika perangkat memerlukannya.


Tombol **Hapus aplikasi** pada setiap kartu meminta pengetikan nama lengkap `owner/repo`. Penghapusan menghapus project Vercel yang terhubung ke repo pada akun/team yang dikonfigurasi, project Vercel bernama stabil buatan DevControl, repo GitHub, arsip ZIP aplikasi dan update diri terkait beserta thumbnail di R2, serta metadata services, thumbnail, API terkelola, dan pipeline di D1. Project Vercel juga menghapus deployment, domain, variabel lingkungan, dan pengaturannya. Jika salah satu provider gagal, dialog menampilkan tahap yang gagal dan tombol dapat dicoba lagi; arsip dan metadata yang belum selesai tetap tersedia untuk pengulangan. Repo yang sedang dideploy atau project Vercel DevControl aktif ditolak. Token GitHub harus memiliki izin menghapus repo (classic `delete_repo` atau fine-grained Administration write); `VERCEL_TOKEN` harus dapat menghapus project pada `VERCEL_TEAM_ID` yang sesuai. Project di akun/team Vercel lain tidak dapat ditemukan oleh token ini.

Jika kartu repo kosong, buka `/api/github-repos` pada domain DevControl yang aktif. Respons `412` berarti `GITHUB_TOKEN` belum terpasang; respons berisi error GitHub berarti periksa token dan hak akses; respons `[]` berarti token berfungsi tetapi belum melihat repo tersebut (misalnya token fine-grained hanya diberi akses ke repo tertentu atau pemilik repo yang berbeda). Periksa `GITHUB_TOKEN` pada environment Vercel yang sedang dipakai dan lakukan redeploy setelah mengubahnya. Halaman Projects dan modal Update Diri kini menampilkan kesalahan dari API secara jelas. Jangan pernah memasukkan token GitHub ke URL atau membagikannya di tangkapan layar.

Project production dibuat dari ZIP melalui API Vercel. Untuk aplikasi yang membutuhkan variabel lingkungan saat build atau runtime, atur variabel tersebut pada project Vercel target. Build yang berhasil tidak menjamin fungsi aplikasi dengan layanan eksternal telah dikonfigurasi.

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
