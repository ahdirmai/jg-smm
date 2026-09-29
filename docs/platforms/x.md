# Platform Brief — X (Twitter)

> **Platform:** X (Twitter) (`X`) · **Status:** deferred (slot siap) · **Brief versi:** `v0.0` · **Update:** —
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

- **Actor:** `apidojo/twitter-scraper-lite` (Apify id `nfp1fpt5gUlBwPcor`) — id full, bukan turunan `APIFY_ACTOR_PREFIX`, seperti actor platform lain. Run mode sync (`run-sync-get-dataset-items`). Semua angka di bawah diukur dari run live 2026-09-29; X masih `deferred`, jadi belum ada env/kode untuk actor ini.
- **Input:** hanya dua key yang benar-benar dikirim — `searchTerms` (array) + `sort: "Latest"` — dan tidak ada yang lain.
  - **⚠ Tidak ada batas hasil dan tidak ada input tanggal sama sekali.** `maxItems` bukan key di schema actor ini, jadi mengirimkannya adalah key yang diabaikan: ia terbaca seperti filter yang bekerja, padahal tidak. Yang tersisa hanya keyword yang lebih sempit dan hasil yang lebih sedikit — dan tidak ada field payload yang bisa dipersempit, jadi window 100% ditegakkan ingestor.
  - **⚠ Tidak ada actor per-post/URL.** Satu-satunya mode adalah keyword search, jadi "scrape satu tweet dari URL" tetap ⬜ di `../PLATFORM_MATRIX.md` §2.1 dan butuh actor terpisah, bukan flag di sini.
  - **Biaya — $0.0004 per hasil PLUS ~$0.016 per QUERY.** Run terukur 10 baris ditagih $0.020: biaya hasil cuma $0.004, selebihnya tarif query. Batch 5 keyword sudah ~$0.08 sebelum satu baris pun datang, dan angka itu tidak terlihat di halaman store. Tanpa batas hasil dan tanpa filter tanggal, satu batch X bisa mengembalikan pool penuh dan membakar sisa kredit bulan ini — perlakukan sebagai platform **paling berbahaya secara biaya, bukan yang termurah**.
- **Output field:** row-nya adalah objek tweet API v2 apa adanya — `id`, `text` (dengan `fullText` identik), `url` (x.com) + `twitterUrl`, `createdAt`, metrik top-level `likeCount`/`retweetCount`/`replyCount`/`viewCount`, plus `quoteCount`/`bookmarkCount`; `author{type,userName,url,twitterUrl,id,name,isVerified,isBlueVerified}`; `media[]` dan `videos[]` sebagai dua array terpisah (keduanya kosong pada row yang tertangkap); `entities.hashtags[]`, `extendedEntities`, `card`, `place`, `conversationId`, `lang`, `source`, dan flag `isRetweet`/`isReply`/`isQuote`/`isPinned`/`isConversationControlled`.
  - `author.userName` **tanpa** `@` di depan — berbeda dari `channel.username` TikTok yang sudah membawa `@`. Jangan menambahkan `@` lagi saat menyusun ulang handle.
  - **Tanggal — format lama Twitter, bukan RFC3339.** `createdAt` datang sebagai `"Sun Sep 27 13:23:06 +0000 2026"`, yaitu Go layout `Mon Jan 2 15:04:05 -0700 2006`. `parseActorTime` tidak punya layout itu, jadi ia mengembalikan **zero time tanpa error**.
    - **Kegagalan ini senyap dan fatal.** `inWindow` justru **mempertahankan** timestamp yang nol (kegagalan field, bukan data), sehingga setiap post X lolos ke window apa pun, lalu `publishedAtPtr` menyimpan kolomnya sebagai **NULL**. Hasilnya satu batch penuh post tanpa tanggal yang tidak pernah bisa difilter ulang — bukan error, bukan baris yang hilang, dan baru terlihat jauh kemudian, saat post itu keluar dari daftar hasil filter tanggal.
    - Parser X butuh layout khusus. Menambahkannya ke `parseActorTime` juga boleh, selama layout yang sudah dipakai platform lain tidak ikut berubah.
  - Tidak ada comment body di row ini — hanya `replyCount` — jadi "Scrape comments" tetap ⬜ dan butuh actor terpisah.
- **Normalisasi → DB:** `id`→`Post.external_id`, `text`→`text`, `author.userName`→`author_handle`, `author.id`→`author_id`, `media[]` + `videos[]`→`media_urls` (slice non-nil, `[]` saat kosong), `createdAt` (layout khusus di atas)→`Post.published_at` (migration `000016`).
  - Metrics dipetakan ke key kanonik: `likeCount`→`likes`, `retweetCount`→`shares`, `replyCount`→`comments`, `viewCount`→`views`.
    - **`replyCount` wajib di-rename jadi `comments`.** Snapshot writer hanya membaca `("comments", "commentCount")`, jadi row yang dipetakan apa adanya tersimpan dengan comment count nol pada post yang selebihnya terlihat benar. `likeCount`, `retweetCount`, dan `viewCount` kebetulan sudah menjadi alias yang dibaca writer, tapi tetap simpan sebagai key kanonik supaya tidak bergantung pada daftar alias itu.
    - **`quoteCount` dan `bookmarkCount` dibuang, bukan disimpan dengan nama baru.** Tidak ada kolom snapshot maupun renderer untuk keduanya; key asing akan tersimpan lalu dibaca tidak oleh siapa pun — persis kelas kegagalan yang pernah menimpa `views` di actor sebelumnya.

## 5. Live Brief (bila ada)

- **Sumber:** `<...>`
- **Cara ambil live views:** `<...>`

## 6. Risiko & Catatan Platform

- X: post, reply, repost/quote, like; anti-automation ketat → risiko ban tinggi. Belum ada brief.
- Belum diaktifkan. Brief ditulis saat rollout R2.
- Kebijakan platform: `<...>`
- Sinyal ban / rate limit khusus: `<...>`
- Perbedaan penting vs platform lain: `<...>`

## 7. Riwayat Brief

| Versi | Tanggal | Perubahan |
| --- | --- | --- |
| v0.0 | | Stub dibuat |
