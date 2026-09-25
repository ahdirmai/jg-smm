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

### 4.2 Keyword search scrape — `apify/instagram-scraper` (`searchType:hashtag`)

> **Jangan pakai `apify/instagram-search-scraper`.** Actor itu me-resolve keyword
> jadi *halaman place/hashtag*, lalu mengembalikan top-post milik entitas itu
> (untuk keyword hidup: post 2018–2024), dan field input-nya `search` (string
> koma), bukan `searchTerms[]`. Dengan `searchTerms[]` actor mengabaikan keyword
> dan memakai default bawaannya (`"restaurant, restaurant prague"`) — silent,
> tanpa error. Actor itu juga tidak punya batas tanggal atas.

- **Input:** `search` (string, keyword digabung koma — BUKAN array), `searchType:"hashtag"`, `resultsType:"posts"`, `resultsLimit`, `searchLimit:1`, `onlyPostsNewerThan` (batas bawah saja).
- **Run mode:** sync (`run-sync-get-dataset-items`).
- **⚠ Tidak ada batas atas di actor** → window bagian atas diterapkan di ingestor (`inWindow`).
- **Output row:** satu row **flat per post** (bentuk sama dengan `instagram-post-scraper`): `shortCode`, `type`, `caption`, `ownerUsername`, `ownerId`, `displayUrl`/`images[]`, `likesCount`, `commentsCount`, `videoViewCount`, `timestamp` (RFC3339), `hashtags[]`, `latestComments[]`.
- **Row metadata hashtag ikut terkirim** (`searchTerm`, `searchSource`, `name`, `postsCount` tanpa `posts[]`) — diabaikan parser, bukan error.
- **Normalisasi → DB:** `shortCode`→`Post.external_id`, `ownerUsername`→`author_handle`, `caption`→`text`, `displayUrl`→`media_urls`, `likesCount`/`commentsCount`/`videoViewCount`→`Post.metrics` + `MetricSnapshot`.
- **Parser lama (§ row `posts[]` nested)** tetap dipertahankan: bentuk itu masih muncul untuk row detail place/hashtag. Lihat `parseIGSearchItems`.
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
