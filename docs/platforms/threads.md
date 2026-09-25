# Platform Brief — Threads

> **Platform:** Threads (`THREADS`) · **Status:** AKTIF (MVP) · **Brief versi:** `v0.0` · **Update:** —
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

### 4.1 On-demand post scrape — `apify/meta-threads-scraper`

- **Actor:** `apify/meta-threads-scraper` (mode user-posts; runner me-resolve username dari permalink).
- **Input:** `<url | handle | hashtag | mention>`
- **Output field:** `<...>`
- **Normalisasi → DB:** `<Post / Comment / MetricSnapshot mapping>`

### 4.2 Keyword search scrape — `futurizerush/meta-threads-scraper` (`mode:search`)

- **Input:** `mode:"search"`, `keywords[]` (array — beda dari IG yang string koma), `max_posts`, `search_filter:"recent"`, `start_date`, `end_date`.
- **Run mode:** sync (`run-sync-get-dataset-items`).
- **Window:** kedua batas didukung actor (`start_date`/`end_date`, inklusif) — beda dari IG. Ingestor tetap menerapkan `inWindow` sebagai jaring pengaman.
- **Output row:** satu row flat per post — `record_type`, `post_code`, `post_url`, `text_content`, `created_at` (RFC3339), `like_count`, `reply_count`, `repost_count`, `quote_count`, `share_count`, `view_count`, `username`, `user_id`, `media_urls[]`, `search_keyword`.
- **Normalisasi → DB:** `post_code`→`Post.external_id`, `username`→`author_handle`, `text_content`→`text`, `media_urls[]`→`media_urls`, metrics `like_count`/`reply_count`/`view_count`/`share_count` (fallback `repost_count`→`quote_count`).
- **Catatan:** `mode` default actor adalah `user` (user-posts). Tanpa `mode:"search"` keyword diabaikan.

## 5. Live Brief (bila ada)

- **Sumber:** `<...>`
- **Cara ambil live views:** `<...>`

## 6. Risiko & Catatan Platform

- Threads: post, reply, repost (repost didukung); login interaktif; rate limit 15/jam.
- Action via Playwright. Selector & langkah detail diisi saat implementasi P3-05.
- Kebijakan platform: `<...>`
- Sinyal ban / rate limit khusus: `<...>`
- Perbedaan penting vs platform lain: `<...>`

## 7. Riwayat Brief

| Versi | Tanggal | Perubahan |
| --- | --- | --- |
| v0.0 | | Stub dibuat |
