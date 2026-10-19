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

### 4.1 On-demand post scrape — `themineworks/threads-scraper` (`mode:post`)

- **Actor:** `themineworks/threads-scraper` — **full id, bukan turunan `APIFY_ACTOR_PREFIX`**. Env `APIFY_POST_ACTOR_THREADS`. Dua pendahulunya sudah tidak dipakai: `futurizerush/meta-threads-scraper` **404** (build user-posts-only, tidak punya mode per-post), dan `pro100chok/threads-scraper-usage` hanya bisa async (tidak cocok dengan `run-sync-get-dataset-items`).
- **Input:** `mode:"post"`, `postUrls:[<permalink>]`, `maxPosts`, `includeReplies:true`.
- **`includeReplies:true` wajib.** Default actor mengecualikan reply, dan reply adalah satu-satunya sumber comment Threads. Tanpa flag ini post tersimpan dengan 0 comment.
- **Context row:** mode post juga mengembalikan share/quote context row di sekitar post yang diminta (`Dengan <name> – Saya baru saja diakui…`), tanpa field yang membedakannya dari post asli. Ingestor memakai shortcode dari permalink untuk men-drop semua kecuali post yang diminta.
- **Output row:** satu row flat per post — `code`, `url`, `text`, `posted_at` (RFC3339), `like_count`, `reply_count`, `repost_count`, `quote_count`, `username`, `media_urls[]`, `is_reply`, `is_quote_post`, `replied_to_post_code`.
- **Normalisasi → DB:** `code`→`Post.external_id`, `username`→`author_handle`, `text`→`text`, `media_urls[]`→`media_urls`, metrics `like_count`/`reply_count`/`repost_count`+`quote_count`.

> **Perbaikan (v0.3):** mode post live mengirim `is_reply:true` dengan `parent_post_id:null` pada SETIAP row reply (diverifikasi pada run `x2Ts33gUPTXBNd6GT`, 24 row). Parser lama hanya mengandalkan map `parent_post_id`, sehingga permalink scrape menyimpan post dengan **0 comment**. Untuk scrape per-post (`onlyCode` diisi) parent sekarang di-fallback ke shortcode yang diminta — semua reply dalam run itu hanya bisa milik post tersebut.
>
> **Kesalahan terdokumentasi (v0.2):** actor flag `is_reply` pada row yang parent-nya **tidak pernah dikirim** — mode keyword menandai reply tanpa mengambil parent, dan `replied_to_post_code` datang kosong. Parser lama men-drop row itu sebagai orphan, sehingga 19 dari 50 post batch keyword hilang. Sekarang row tanpa parent yang bisa di-resolve disimpan sebagai post top-level.

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
| v0.3 | 2026-10-19 | Scrape per-post: `parent_post_id` null pada row `is_reply` → parent di-fallback ke shortcode permalink yang diminta (sebelumnya 0 comment). Comment permalink Threads = `/post/<comment code>` (rute `/c/` 404). Reply dari dashboard bisa memakai link komentar yang di-paste. |
| v0.2 | 2026-09-29 | §4.1 actor per-post diganti: `apify/meta-threads-scraper` (404 — build user-posts-only, tidak ada mode per-post) → `pro100chok/threads-scraper-usage` (async-only) → `themineworks/threads-scraper`. Id full, bukan turunan prefix. `includeReplies:true` wajib (reply = satu-satunya sumber comment). Ingestor sekarang memfilter context row share/quote via shortcode permalink, dan menyimpan row `is_reply` tanpa parent sebagai post (sebelumnya di-drop — 19/50 post batch keyword hilang). |
