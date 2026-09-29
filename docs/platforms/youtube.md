# Platform Brief — YouTube

> **Platform:** YouTube (`YOUTUBE`) · **Status:** deferred (slot siap) · **Brief versi:** `v0.0` · **Update:** —
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

- **Actor:** `apidojo/youtube-scraper-api` (Apify id `gXSReGYeawn5nwDhI`) — **full id, bukan turunan `APIFY_ACTOR_PREFIX`**, sama seperti tiga actor keyword lain. Env `APIFY_SEARCH_ACTOR_YOUTUBE`.
  - **Run mode:** sync (`run-sync-get-dataset-items`).
  - **Biaya — bukan cuma per hasil, ada juga event per QUERY.** Harga yang tertera di halaman store adalah harga tier diskon; yang benar-benar ditagih akun FREE plan adalah `eventTieredPricingUsd.FREE.tieredEventPriceUsd`, yaitu **~$0.0005 per hasil PLUS ~$0.015 per query**. Run terukur 9 baris dibayar **$0.0185** — hampir seluruhnya ongkos query, bukan data.
    - Hitung ongkos batch dari atas, bukan dari `maxItems`: 5 keyword = 5 query = **~$0.075 ongkos query sebelum satu baris pun kembali**, padahal biaya 5 keyword × 30 video hanya $0.075 juga. Jadi menggandakan keyword menggandakan ongkos query, bukan cuma baris.
- **Input:** **keyword saja** — `{"keywords": [...], "maxItems": N, "uploadDate": "all|l|t|w|m|y"}`. Tidak ada input lain yang dikirim.
  - **⚠ Tidak ada mode URL sama sekali.** Actor ini tidak punya mode per-post, per-channel, per-handle, maupun hashtag — `Scrape post by URL` masih ⬜ di `../PLATFORM_MATRIX.md` §2.1 dan **tidak ada flag yang bisa dinyalakan** di sini, karena jalur itu memang tidak ada di skemanya, bukan disembunyikan. Butuh satu permalink = actor terpisah, bukan field baru di payload ini.
  - **`uploadDate` adalah enum durasi, bukan tanggal.** Nilai yang diizinkan hanya `all`, `l`, `t`, `w`, `m`, `y`. Mengirim string tanggal asli — atau kata `"month"` — ditolak outright dengan HTTP 400 `invalid-input: Field input.uploadDate must be equal to one of the allowed values: "all", "l", "t", "w", "m", "y"`. Kode kita memetakan batas bawah window ke **ember paling kasar yang masih memuat seluruh rentang**, lalu ingestor mempersempit ke hari yang tepat: ember yang terlalu sempit diam-diam membuang post yang operator minta, ember yang terlalu lebar hanya mahal.
  - `maxItems` adalah **batas nyata atas baris yang dikembalikan**, jadi itulah plafon biaya per-hasil satu run.
- **Output field:** satu row per video, 30 key. Row ini **butuh parser sendiri** (`parseYouTubeItems`) karena `channel` dan `thumbnails` masing-masing bersarang satu level.
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

- YouTube: comment butuh state channel login; video-centric. Belum ada brief.
- Belum diaktifkan. Brief ditulis saat rollout R4.
- Kebijakan platform: `<...>`
- Sinyal ban / rate limit khusus: `<...>`
- Perbedaan penting vs platform lain: `<...>`

## 7. Riwayat Brief

| Versi | Tanggal | Perubahan |
| --- | --- | --- |
| v0.0 | | Stub dibuat |
