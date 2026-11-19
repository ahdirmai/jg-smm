# Platform Brief — Instagram

> **Platform:** Instagram (`INSTAGRAM`) · **Status:** AKTIF (MVP) · **Brief versi:** `v0.2` · **Update:** 2026-09-23
>
> Panduan: isi tiap bagian saat platform diaktifkan. Sinkronkan kapabilitas dengan `../PLATFORM_MATRIX.md` §2. **Semua action via Playwright** (bukan API/OAuth).

## 1. Kapabilitas

| Action | Didukung | Brief tersedia |
| --- | --- | --- |
| Comment on post | ☐ | ☐ |
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

- **Halaman:** `<url>`
- **Step:** `<isi username → password → submit → ???>`
- **Sinyal sukses (bukti):** `<cookie X / elemen Y>` (bukan redirect URL).
- **2FA / checkpoint:** `<alur, kondisi NEEDS_INPUT>`
- **Catatan fingerprint:** UA/locale/viewport; khusus platform ini: `<...>`

## 3. Action Brief (isi per action yang didukung)

### 3.1 `<ACTION>` — `<Comment on post>`

- **Alur UI:**
  1. `<buka target URL>`
  2. `<klik tombol komentar>`
  3. `<isi teks → submit>`
- **Selector (`SEL`):**

  | Nama | Selector | Catatan |
  | --- | --- | --- |
  | commentButton | `` | |
  | composerInput | `` | |
  | submitButton | `` | |
- **Verifikasi ground truth:** `<cara memastikan komentar muncul (poll ≤ 15 dtk)>`
- **Rate limit / jeda:** `<x/jam, jitter>`
- **Risiko:** `<...>`

### 3.2 `<ACTION>` — `<...>`

`<ulangi struktur 3.1>`

## 4. Scrape Brief (Apify)

### 4.1 On-demand post scrape — `~smm/instagram-post-scraper`

- **Input:** post permalink di field `username` (actor pakai nama itu), `resultsLimit`.
- **Run mode:** sync (`run-sync-get-dataset-items`).
- **Output row:** satu row per post — `shortCode`, `caption`, `ownerUsername`, `ownerId`, `images[]`, `latestComments[]`, `likeCount`, `commentCount`.
- **Normalisasi → DB:** `shortCode`→`Post.external_id`, `ownerUsername`→`author_handle`, `caption`→`text`, `images`→`media_urls`, `likeCount`/`commentCount`→`Post.metrics` + `MetricSnapshot`; `latestComments[]`→`Comment` rows keyed by shortcode parent.

### 4.2 Keyword search scrape — `sasky/instagram-keyword-posts-urls-scraper`

- **Actor:** `sasky/instagram-keyword-posts-urls-scraper` (env `APIFY_SEARCH_ACTOR_INSTAGRAM`). Id full, bukan turunan `APIFY_ACTOR_PREFIX`.
- **Dua tahap:** actor search hanya mengembalikan **URL post**, tanpa caption/metrics. Setiap URL di-enrich lewat `apify/instagram-post-scraper` (§4.1), dedup per URL agar post yang muncul di dua keyword tidak dibayar dua kali. Post yang tidak bisa di-enrich (dihapus/private) dilewati, bukan disimpan kosong.
- **Input yang benar-benar dikirim:** `keywords` (array) + `limit` sebagai **string** (`runner.go`), karena schema actor memakai string; integer diabaikan diam-diam dan run memakai default besar.
- **Run mode:** sync (`run-sync-get-dataset-items`).
- **⚠ Tidak ada batas tanggal yang andal** — `date`/`from`/`to` gagal saat runtime ("Context: Parsing date..") di semua kombinasi yang dicoba, jadi window diterapkan di ingestor (`inWindow`), bukan di payload.
- **Output row search:** URL post; **output row enrichment** mengikuti bentuk §4.1.
- **Normalisasi → DB:** hasil enrichment: `shortCode`→`Post.external_id`, `ownerUsername`→`author_handle`, `caption`→`text`, `images`→`media_urls`, metrics→`Post.metrics` + `MetricSnapshot`.
- **`published_at` → `Post.published_at` (migration `000016`).** Nilai ini dipakai `inWindow` untuk menyaring lalu ikut tersimpan. `Post.scraped_at` tetap `now()` dan bukan pengganti — UI melabelinya `scrape …`.
>
> **Yield.window.** Sejak actor search hanya memberi URL, biaya search dan biaya enrichment terpisah. `limit` adalah batas URL per keyword; window tetap disaring di ingestor. Ukur hasil per run, jangan asumsikan angka yield actor lama.
- **Dataset keys:** `apify/search-<runID>/<i>.json`.

## 5. Live Brief (bila ada)

- **Sumber:** `<...>`
- **Cara ambil live views:** `<...>`

## 6. Risiko & Catatan Platform

- IG: feed, reels, comment; login interaktif headful; rate limit 30/jam.
- Action via Playwright. Selector & langkah detail diisi saat implementasi P3-04.
- Kebijakan platform: `<...>`
- Sinyal ban / rate limit khusus: `<...>`
- Perbedaan penting vs platform lain: `<...>`

## 7. Riwayat Brief

| Versi | Tanggal | Perubahan |
| --- | --- | --- |
| v0.0 | | Stub dibuat |
| v0.1 | 2026-09-23 | §4.1–4.2: scrape brief on-demand (`instagram-post-scraper`) + keyword search (`instagram-search-scraper`) sesuai output asli actor; catat bahwa actor search mengabaikan since/until (filter di ingestor) dan row page kosong bukan error |
| v0.2 | 2026-09-23 | Fix Bad Gateway saat scrape post: (1) actor id ke Apify pakai `~` bukan `/` (`apify/instagram-post-scraper` → 404, `apify~instagram-post-scraper` → 200); (2) read-back post pertama kali via shortcode-dari-URL karena target di-key by URL sedangkan post di-key by shortCode dan LinkTargetPost belum jalan |
| v0.3 | 2026-09-25 | §4.2 koreksi actor: `instagram-search-scraper` → `apify/instagram-scraper` (`searchType:hashtag`). Actor lama me-resolve keyword jadi place/hashtag dan mengembalikan top-post lama; field-nya `search` (koma), bukan `searchTerms[]`, sehingga keyword kita diabaikan tanpa error. Catat tidak ada batas atas → filter di ingestor. Actor id search sekarang env (`APIFY_SEARCH_ACTOR_*`), bukan turunan prefix |
| v0.4 | 2026-09-29 | §4.2 actor search diganti lagi: `apify/instagram-scraper` → `crawlerbros/instagram-keyword-search-scraper`. Row flat **snake_case** (`shortcode`/`pub_date`/`like_count`), bukan camelCase, sehingga butuh parser sendiri (`parseIGFlatPostItems`) — struct camelCase menghasilkan metrics null + handle `unknown`. `pub_date` datang tanpa offset, dibaca UTC. Actor hanya mengembalikan `comment_count`, bukan bodies — comment butuh post-scrape (§4.1) |
| v0.5 | 2026-11-19 | §4.2 actor search diganti ke `sasky/instagram-keyword-posts-urls-scraper`: publisher `crawlerbros` dihapus dari Store (semua batch 403). Actor baru hanya mengembalikan URL, lalu di-enrich `apify/instagram-post-scraper` dan dedup per URL. `limit` dikirim sebagai string; tanggal tetap disaring di ingestor karena input tanggal actor gagal runtime |
