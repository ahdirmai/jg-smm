# Platform Brief — Facebook

> **Platform:** Facebook (`FACEBOOK`) · **Status:** deferred (slot siap) · **Brief versi:** `v0.0` · **Update:** —
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

### 4.1 Keyword search scrape — `danek/facebook-search-ppr`

- **Actor:** `danek/facebook-search-ppr` (Apify id `l6CUZt8H0214D3I0N`) — **full id, bukan turunan `APIFY_ACTOR_PREFIX`**, seperti tiga actor keyword lain. Env `APIFY_SEARCH_ACTOR_FACEBOOK`. Run mode sync (`run-sync-get-dataset-items`), parser sendiri `parseFacebookItems` — `author`/`image`/`video`/`video_thumbnail` nested, dan unmarshal case-insensitive milik Go tidak menjembatani tingkat nesting.
  - **⚠ Yang paling terbukti bukan yang tepat.** `apify/facebook-posts-scraper` adalah actor Facebook paling mapan di store (115.277 user, 234 review, 4.64) — dan **tidak bisa dipakai di sini**: inputnya **URL page/profile, bukan keyword**, jadi tidak ada satu pun batch keyword yang bisa dilayaninya. Reputasi store tidak menjawab kecocokan input; yang menjawab adalah bentuk payload.
  - Row ini butuh parser sendiri juga karena `post_id` datang sebagai **string** (`"1501360315021356"`), bukan number — dibaca `int64` akan kehilangan id di atas 2^53 dan `float64` membulatkan sebagiannya.
- **Input:** satu **string** `query` (keyword kita digabung spasi, tanpa filter — actor ini tidak menerima array dan pencocokannya sendiri yang menentukan pool), `search_type:"posts"`, `max_posts`.
  - `search_type` harus **eksplisit**: actor ini juga mencari grup, orang, dan event, dan default-nya bukan posts. Tanpa flag ini pool berisi baris yang bukan post.
  - `max_posts` adalah **batas nyata**, bukan ukuran halaman.
  - **Tidak ada input tanggal sama sekali** — tidak ada padanan `since`/`until`. Kedua batas window diterapkan di ingestor (`inWindow`), jadi itulah satu-satunya filter antara yang kita bayar dan yang kita simpan; persempit keyword, bukan `max_posts`, yang memperbaiki yield.
  - **Tidak ada mode per-post/URL di codebase ini.** `Scrape post by URL` masih ⬜ di `../PLATFORM_MATRIX.md` §2.1, dan actor ini memang tidak punya mode URL — jadi tidak ada parser maupun gate-nya, karena tidak ada jalur yang bisa di-gate. Mengambil satu permalink adalah actor terpisah, bukan flag di sini.
- **Output field:** satu row per post, 31 key — `post_id`, `type` (`"post"` atau `"video"`; **video tetap post yang kita mau**), `url` (permalink; bukan external id), `message` + `message_rich` (fallback, `message` bisa null/kosong di post foto- atau link-only), `timestamp` (**unix detik**, bukan milidetik), `reactions_count`, `comments_count`, `reshare_count`, `image{uri}`, `album_preview[{type, image_file_uri}]`, `video{uri}`, `video_files[{uri}]`, `video_thumbnail{uri}`, `video_view_count`, `external_url`, `author{id, name, url, profile_picture_url}`, `author_title` (null), `authors[]`, `associated_group`, `attached_post`, `top_comments`, `shares_id`, `comments_id`, `reactions`, `text_format_metadata`, plus `error` untuk row gagal tingkat actor (`no_items`, `not_found`).
  - **⚠ `album_preview` adalah sumber media yang nyata, dan tidak boleh diabaikan.** Terukur pada run hidup 2026-09-29: post dengan `image` kosong tetap memuat gambarnya di `album_preview[].image_file_uri`. Mengabaikannya menyimpan post dengan **nol media** — kelas bug yang sama dengan actor TikTok sebelumnya, yaitu post yang tampil sebagai baris teks telanjang bagi semua pembaca di hilir.
  - **`image` dibaca dari `.uri` yang non-kosong, bukan dari `image != nil`.** Pada row tanpa gambar ia datang sebagai objek kosong `{}`, bukan selalu `null`, sehingga pointer non-nil dengan `uri` kosong tetap lolos cek dan menyimpan media yang tidak ada.
  - **⚠ Jebakan pfbid pada handle.** Handle normal adalah **segmen path terakhir** dari `author.url` (`https://www.facebook.com/rudi.andika.182` → `rudi.andika.182`). Tapi `author.url` milik sebuah **Page** berbentuk `https://www.facebook.com/people/<Display>/pfbid02…`, dan pfbid itu **identik dengan `author.id`** — jadi segmen terakhir bukan handle, melainkan salinan id yang tidak memberi informasi apa pun. Handle ada di segmen sebelumnya: `Muhammad-Saidi`.
  - **Tidak ada view count.** Tidak ada key `views` sama sekali, dan `video_view_count` datang `null` pada row yang diuji. Facebook tidak mengukur tayangan di hasil pencarian.
- **Normalisasi → DB:** `post_id`→`Post.external_id` (fallback terakhir: id dibaca dari permalink — `/posts/<id>`, `/videos/<id>`, atau parameter `v` di `/watch/`), handle turunan di atas→`author_handle`, `author.id`→`author_id` (fallback ke `post_id`), `message`/`message_rich`→`text`, `timestamp`→`Post.published_at` (unix **detik**; menyalin konversi ms akan mendate-kan semua post FB ke 1970 lalu membuangnya sebagai di luar window), dan metrics: `reactions_count`→**`likes`**, `comments_count`→`comments`, `reshare_count`→`shares` → `Post.metrics` + `MetricSnapshot`.
  - **`reactions_count` harus disimpan sebagai `likes`.** Snapshot writer membaca key kanonik `likes`; key `reactions` akan tersimpan lalu dibaca tidak oleh siapa pun. Facebook menamainya reaction, sistem menamainya likes.
  - **Key `views` tidak pernah ditulis, di jalur mana pun.** Menyimpan `views: 0` akan dibaca snapshot writer sebagai pengukuran sungguhan — dan karena `TopPostsByMetric` mengurutkan kolom `metric_snapshot.views`, **setiap** post Facebook akan menempati peringkat terakhir di belakang post yang memang tidak punya view. Tidak ada lebih baik daripada tidak ada.
  - Metrik ikut berdasarkan **kehadiran key, bukan nilai bukan-nol**: post dengan 0 komentar memang dilaporkan 0, jadi key `comments: 0` itu nyata dan harus ikut tersimpan. Key yang diomit row berarti tidak diketahui, bukan nol.
  - `media_urls` **selalu non-nil**: `video_files[]` (file playable) lebih dulu, `video_thumbnail.uri` hanya sebagai fallback frame, lalu `image.uri`, lalu seluruh `album_preview[].image_file_uri`, dan `external_url` sebagai akhir supaya post link tidak pernah tanpa thumbnail. Array kosong berarti tidak ada media — `null` dan `[]` berbeda bagi semua pembaca hilir.
  - Baris dengan `type` selain `post`/`video` di-drop diam-diam (bukan row rusak, cuma bukan post yang kita mau); `type` kosong **dipertahankan**, karena field yang absen adalah kekurangannya, bukan bukti baris non-post. Row `error` gagal keras, dan row tanpa `post_id` maupun id di permalink juga gagal keras — lebih baik bilang begitu daripada menyimpan post dengan key kosong.
- **Biaya — terukur, bukan dari halaman store.** Satu run 5 row ditagih **$0.00005**, sekitar **$0.00001 per baris**: dua orde besaran di bawah apa yang mengesankan listing store, dan tanpa add-on yang perlu dinonaktifkan.

## 5. Live Brief (bila ada)

- **Sumber:** `<...>`
- **Cara ambil live views:** `<...>`

## 6. Risiko & Catatan Platform

- FB: post, comment, reaction; grup & page berbeda alur. Belum ada brief.
- Belum diaktifkan. Brief ditulis saat rollout R1.
- Kebijakan platform: `<...>`
- Sinyal ban / rate limit khusus: `<...>`
- Perbedaan penting vs platform lain: `<...>`

## 7. Riwayat Brief

| Versi | Tanggal | Perubahan |
| --- | --- | --- |
| v0.0 | | Stub dibuat |
