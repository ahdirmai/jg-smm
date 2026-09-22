# Platform Brief — Instagram

> **Platform:** Instagram (`INSTAGRAM`) · **Status:** AKTIF (MVP) · **Brief versi:** `v0.1` · **Update:** 2026-09-23
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

### 4.2 Keyword search scrape — `~smm/instagram-search-scraper`

- **Input:** `searchTerms[]` (≤5), `maxItems`, `proxyGroups:["RESIDENTIAL"]`, `since`, `until`.
- **Run mode:** sync (`run-sync-get-dataset-items`).
- **⚠ Actor MENGABAIKAN `since`/`until`** → filter window diterapkan di ingestor (`inWindow` pada `taken_at`), bukan di actor.
- **Output row:** satu row per page ter-resolve (lokasi/hashtag) — `searchTerm`, `searchSource`, `name`, `postsCount`, `posts[]` nested. Page kosong (`postsCount:0`, tanpa `posts[]`) = pencarian sukses kosong, BUKAN error.
- **Isi `posts[]`** (raw Instagram media-dict): `code`, `pk`, `taken_at` (unix), `caption.text`, `like_count`, `comment_count`, `view_count`/`play_count`, `user.{username,pk}`, `image_versions2.candidates[]` (elemen pertama = varian terbesar), `carousel_media[]` (satu image/video per entry via `image_versions2`/`video_versions`), `display_uri` fallback.
- **Normalisasi → DB:** `code`→`Post.external_id`, `user.username`→`author_handle`, `user.pk`→`author_id`, `caption.text`→`text`, media dari `carousel_media[]` (jika ada) else `image_versions2.candidates[0]` else `display_uri`; metrics `like_count`/`comment_count`/`view_count` (reels: `play_count`→`views`). Tidak ada array komentar di row search — hanya `comment_count`.
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
