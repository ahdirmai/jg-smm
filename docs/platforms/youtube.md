# Platform Brief — YouTube

> **Platform:** YouTube (`YOUTUBE`) · **Status:** login + session + comment live; like/reply deferred · **Brief versi:** `v0.2` · **Update:** 2026-11-17
>
> Panduan: isi tiap bagian saat platform diaktifkan. Sinkronkan kapabilitas dengan `../PLATFORM_MATRIX.md` §2. **Semua action via Playwright** (bukan API/OAuth).

## 1. Kapabilitas

| Action | Didukung | Brief tersedia |
| --- | --- | --- |
| Comment on post | ✅ | ✅ |
| Reply comment | ☐ | ☐ |
| Like post | ☐ | ☐ |
| Like comment | ☐ | ☐ |
| Report post | ☐ | ☐ |
| Report comment | ☐ | ☐ |
| Share | ☐ | ☐ |
| Repost | ☐ | ☐ |
| Views / Reach | ☐ | ☐ |
| Live views | ☐ | ☐ |

## 2. Login Flow

- **Halaman:** `https://accounts.google.com/ServiceLogin?service=youtube&continue=https%3A%2F%2Fwww.youtube.com%2F`
  — `continue` wajib. Google menulis cookie sesinya di `.google.com`; YouTube
  baru menulis cluster-nya sendiri (`LOGIN_INFO`, `SAPISID`, `SID`, `HSID`,
  `SSID`) setelah browser mendarat di `.youtube.com`. `storageState` yang
  diambil di halaman Google saja akan membuka YouTube dalam keadaan logout.
- **Step:** buka halaman → worker **mengisi email otomatis** (autofill) → Google
  sering membalas dengan reCAPTCHA/"Verify it's you" → operator menyelesaikannya
  di live view → worker **mengisi password otomatis** begitu field password
  muncul (fill berjalan paralel dengan poll, jendela 10 menit) → redirect ke
  `youtube.com` → YouTube menulis cookie sesi. Tidak ada autofill untuk kode
  2FA; kode dikirim operator lewat `auth-input` atau diketik di noVNC.
- **Sinyal sukses (bukti):** `LOGIN_INFO` + `SAPISID` ada di cookie jar, **dan**
  `ytcfg.get('LOGGED_IN') === true` di halaman youtube.com. `__Secure-1PSID`
  sendirian **bukan** bukti: cookie itu diterbitkan Google sebelum sesi YouTube
  ada. `handle` yang dilaporkan = nilai `LOGIN_INFO` (bukan kredensial).
- **2FA / checkpoint:** `input[name="idvPin"]` atau
  `input[autocomplete="one-time-code"]` → status `NEEDS_INPUT`; operator
  menyelesaikan di noVNC atau mengirim kode lewat `auth-input`. Challenge
  “Verify it's you”/CAPTCHA juga `NEEDS_INPUT`, bukan `failed`.
- **Catatan fingerprint:** locale `id-ID`, viewport 1280×800, timezone
  `Asia/Jakarta`, geolocation per akun. YouTube **mengabaikan UA tersimpan**
  (termasuk default `Chrome 131` di kolom `account.user_agent`) dan memakai UA
  asli browser: Google membandingkan UA dengan client hints/build, dan UA
  “Chrome 131” di atas Chromium yang lebih baru adalah inkonsistensi yang
  membuat sign-in ditolak sebagai “not secure”. Launch headful dengan
  `--disable-blink-features=AutomationControlled`. Google mengikat sesi ke
  kombinasi IP + fingerprint: pakai proxy residensial yang sama untuk login dan
  action, dan jangan mengubah timezone antar-run.
- **Rotasi cookie:** Google merotasi cookie selama tab yang login dibiarkan
  terbuka. Simpan `storageState` lalu **tutup browser**; sesi yang dirotasi
  gagal di run berikutnya. Sesi mati → `verify` melaporkan
  `LOGGED_IN=false` → login ulang.
- **Dipalsukan/kedaluwarsa:** cookie Google berumur ~2 tahun, tetapi sesi bisa
  dicabut kapan saja (login dari perangkat lain, perubahan keamanan akun).
  Jangan pernah memperlakukan file session sebagai permanen.
- **Penyimpanan:** local harness → `infra/backup/sessions/session-youtube.json`
  (`docs/local-worker/README.md`); app-worker → tombol **Import session** di
  panel akun (dashboard `/accounts`) atau `POST
  /api/accounts/{id}/session/import` (disimpan terenkripsi di `account_session`),
  atau file `/data/sessions/session-youtube.json` pada worker legacy.
  **Export session** mengunduh sesi akun untuk dipindah ke container/fleet lain.
  Verifikasi bypass: `tsx src/run.ts verify --platform youtube`.

## 3. Action Brief (isi per action yang didukung)

### 3.1 Comment on post — Shorts + watch

- **Alur:** buka target URL → tunggu `ytcfg.get('LOGGED_IN') === true` → buka komentar:
  - `/shorts/<id>`: klik tombol rail `aria-label="View comments"` (panel kanan).
  - `/watch?v=<id>`: scroll window ke bawah; `ytd-comments` bisa sudah ada di DOM
    tetapi masih hidden, jadi `scrollIntoViewIfNeeded` saja tidak memicu load —
    worker menggeser window (`scrollBy`) sampai composer mount. Short yang dibuka
    lewat URL `/watch?v=` tetap memakai jalur ini (bukan panel Shorts).
  - `youtu.be/<id>` dikanonikalisasi ke `www.youtube.com/watch?v=<id>` sebelum navigasi (domain pendek diblokir filter media di sebagian network).
  → klik placeholder `#simplebox-placeholder` → ketik isi ke `#contenteditable-root` (keyboard, bukan set value) → klik `#submit-button button` (fallback `button[aria-label="Comment"]`).
- **Selector:**

  | Nama | Selector | Catatan |
  | --- | --- | --- |
  | commentsPanel | `button[aria-label="View comments"]` | Khusus Shorts; watch page tidak punya tombol ini |
  | composer | `ytd-comment-simplebox-renderer` | Sama di kedua surface |
  | composerInput | `#contenteditable-root` | Di dalam simplebox; `pressSequentially`, bukan `fill` |
  | submitButton | `#submit-button button, button[aria-label="Comment"]` | Filter visible; klik setelah teks masuk |
  | renderedComment | `#content-text` | Hanya ada di komentar yang benar-benar render, bukan di editor |
- **Verifikasi ground truth:** poll `#content-text` sampai teks komentar muncul (≤15 dtk). Editor tidak dihitung sebagai bukti — teks yang masih di composer tidak membuktikan submit.
- **Rate limit / jeda:** mengikuti batas action platform-wide (jitter + velocity gate di control plane); tidak ada jeda lokal khusus YouTube.
- **Risiko:** komentar bisa ditahan YouTube (held for review) sehingga tidak muncul di feed; komentar bisa dinonaktifkan; akun tanpa sesi YouTube yang valid ditolak sebagai `auth:` (non-retryable). Navigasi halaman YouTube memakai bound 90 dtk (default 30 dtk terlalu pendek saat fleet Chromium dingin). Like/reply/like-comment belum dibangun dan tetap melaporkan `not_implemented`.

## 4. Scrape Brief (Apify)

- **Actor keyword:** `streamers/youtube-scraper` (search mode) — **full id**, env `APIFY_SEARCH_ACTOR_YOUTUBE`. Input: `searchQueries[]`, `maxResults`, `dateFilter` (`hour|today|week|month|year`); ingestor mempersempit ke hari yang tepat.
  - **Run mode:** sync (`run-sync-get-dataset-items`).
  - **Biaya:** ~**$0.005 per video** (pay per result); tidak ada event per-query.
- **Actor on-demand (post):** `streamers/youtube-scraper` — full id, env `APIFY_POST_ACTOR_YOUTUBE` (default sama dengan keyword, tetapi resolver terpisah). Dipakai `actorForPlatform` untuk satu URL (`POST /api/scrape/target`), terpisah dari actor keyword. Biaya ~**$0.005 per video**.
  - Input: `{"startUrls":[{"url":"..."}], "maxResults":1}`. **Jangan kirim `maxItems`** — actor ini memakai `maxResults`; satu URL = satu video.
  - URL dinormalkan sebelum dikirim: `youtu.be/<id>` → `/watch?v=<id>`, `/shorts/<id>` tetap, host dipaksa `www.youtube.com`, dan query share (`feature`, `si`, `t`) dibuang. Actor menafsirkan URL ber-query sebagai pencarian dan mengembalikan 10 baris `noResults`; URL bersih mengembalikan satu baris video.
  - Mendukung `watch?v=` dan `/shorts/`; row Shorts tidak membawa `status` (dianggap sehat) dan `type:"shorts"`.
- **Input keyword:** `{"searchQueries": [...], "maxResults": N, "dateFilter": "hour|today|week|month|year"}`. Tidak ada mode per-channel/handle/hashtag di payload keyword.
  - **`uploadDate` adalah enum durasi, bukan tanggal.** Nilai yang diizinkan hanya `all`, `l`, `t`, `w`, `m`, `y`. Mengirim string tanggal asli — atau kata `"month"` — ditolak outright dengan HTTP 400 `invalid-input: Field input.uploadDate must be equal to one of the allowed values: "all", "l", "t", "w", "m", "y"`. Kode kita memetakan batas bawah window ke **ember paling kasar yang masih memuat seluruh rentang**, lalu ingestor mempersempit ke hari yang tepat: ember yang terlalu sempit diam-diam membuang post yang operator minta, ember yang terlalu lebar hanya mahal.
  - `maxResults` (keyword) adalah batas nyata atas baris; on-demand memakai `maxResults:1`.
- **Output field:** parser `parseYouTubeItems` menangani **dua bentuk**:
  - keyword (`streamers/youtube-scraper`): bentuk streamers yang sama (flat, `channelUsername`, `thumbnailUrl`, `date`);
  - on-demand (`streamers/youtube-scraper`): `viewCount`/`likes`/`commentsCount` flat, `channelUsername` tanpa `@`, satu `thumbnailUrl`, `date`.
  Keduanya dinormalisasi ke `scrapeItem` yang sama (id, title, handle, author id, media, views/likes/comments, publishedAt).
  - `id` = **video id polos** (`k_ldBC5mQu8`) — bukan URN `yt:video:…`, bukan channel id; `title` (disimpan sebagai teks post), `description` (body penuh, sengaja tidak dibaca), `url`, `embedUrl`.
  - `uploadDate`, `publishDate`, `relativeDate` = **instant yang sama** dalam RFC3339 **dengan offset** (`2026-09-13T01:25:08-07:00`) — ketiganya bukan tiga sumber tanggal yang berbeda, dan `relativeDate` bukan string "3 hari lalu" seperti namanya.
  - `views`, `likes`, `comments` = int **flat** di atas.
  - `channel{id,name,handle,url,subscriberCount}` — `handle` **sudah membawa `@` di depan** (`@K7_Bakujuk`), dan `subscriberCount` adalah **string dengan satuan di dalamnya** (`21.2K subscribers`), bukan angka: tidak ada kolom subscriber di post, jadi field ini sengaja tidak dipetakan.
  - `thumbnails[]` = array varian ukuran (`url`,`width`,`height`) yang **terurut dari yang terkecil** — varian terbesar dipilih **berdasar luas piksel**, bukan indeks 0.
  - `duration`, `type`, `status` (`"OK"` pada row sehat; `error` menandai baris kegagalan).
  - `streamingData` dan `captions` **sengaja tidak dideklarasikan sama sekali** di struct parser: isinya hanya URL bertanda tangan yang kedaluwarsa dalam hitungan jam, jadi apa pun yang diambil darinya pasti tautan mati.
  - **Tidak ada `shares`, tidak ada `collects`.** Kunci itu tidak ada di actor ini dan tidak boleh dikarang.
- **Normalisasi → DB:** `id`→`Post.external_id`, `title`→`text`, `channel.handle`→`author_handle` (fallback `channel.name`), `channel.id`→`author_id`, **thumbnail terbesar**→`media_urls` (selalu array non-nil, `[]` bila kosong), `uploadDate`/`publishDate`/`relativeDate`→`Post.published_at`; metrics `views`→`views`, `likes`→`likes`, `comments`→`comments` ke `Post.metrics` + `MetricSnapshot`.
  - **Metric yang absen tetap ABSEN, tidak pernah 0.** Yang diuji adalah keberadaan field, bukan nilainya: video dengan 0 komentar memang 0, dan hanya field yang dihilangkan row-lah yang tidak ditulis.
  - **`shares`/`collects` tidak ditulis di jalur kode mana pun.** YouTube tidak menerbitkan hitungan share atau save untuk video, jadi 0 yang dikarang akan dibaca snapshot writer sebagai pengukuran nyata — setiap post YouTube akan peringkat terakhir di metrik itu, di belakang post yang memang tidak punya metrik tersebut sama sekali.

## 5. Live Brief (bila ada)

- **Sumber:** `<...>`
- **Cara ambil live views:** `<...>`

## 6. Risiko & Catatan Platform

- YouTube: comment top-level sudah jalan di Shorts dan watch page (butuh state
  channel login). Like, reply, dan like-comment masih stub `not_implemented`.
  Komentar yang ditahan YouTube tidak akan lolos verifikasi `#content-text`;
  ini kegagalan platform, bukan selector.
- Login Google: autofill mengisi email + password, tetapi Google sering
  memasang reCAPTCHA di antara keduanya. Itu langkah manusia (live view), bukan
  kegagalan worker; setelah challenge selesai password terisi otomatis. Pesan
  “browser or app may not be secure” = Google menolak jalur otomatis; selesaikan
  manual di live view. Jalur tanpa manusia tetap **import session**.
- Sesi Google terikat IP + fingerprint. Ganti proxy/UA/timezone antar-run =
  challenge atau logout paksa. Gunakan proxy residensial sticky yang sama.
- Kebijakan platform: otomasi akun Google melanggar ToS; risiko suspend.
- Sinyal ban / rate limit khusus: challenge “Verify it's you”,
  `accounts.google.com/signin/rejected`, atau logout spontan.
- Perbedaan penting vs platform lain: autentikasi ada di domain Google, sesi
  YouTube ada di cookie domain `.youtube.com`; dua hal berbeda.

## 7. Riwayat Brief

| Versi | Tanggal | Perubahan |
| --- | --- | --- |
| v0.0 | | Stub dibuat |
| v0.1 | 2026-11-17 | Login/session nyata: `continue` ke youtube.com, bukti `LOGIN_INFO`+`SAPISID`, `ytcfg.LOGGED_IN`, alur operator + batas rotasi/fingerprint |
| v0.2 | 2026-11-17 | Comment top-level live di Shorts + watch; on-demand scrape via `startUrls` + kanonikalisasi youtu.be |
| v0.3 | 2026-11-18 | Actor on-demand pindah ke `streamers/youtube-scraper` (satu row, watch+shorts); keyword tetap `apidojo/youtube-scraper`; query share dibuang |
