# Platform Brief — TikTok
> **Platform:** TikTok (`TIKTOK`) · **Status:** AKTIF (MVP, keyword scrape saja) · **Brief versi:** `v0.1` · **Update:** 2026-09-29
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

Action **tidak** dikerjakan di MVP ini. Yang hidup hanya keyword scrape (§4.1). Actor-nya keyword-saja: mode `profiles[]`/`postURLs[]`/`hashtags[]` milik actor sebelumnya **sudah tidak ada**, jadi tidak ada yang belum diintegrasikan — hanya `expansion` yang sengaja dibiarkan mati (§4.1).
## 2. Login Flow
- **Halaman:** —
- **Step:** —
- **Sinyal sukses (bukti):** —
- **2FA / checkpoint:** —
- **Catatan fingerprint:** —
Belum ada flow login TikTok. Dibutuhkan hanya bila action Playwright (§3) dihidupkan.
## 3. Action Brief
Kosong — lihat §1.
## 4. Scrape Brief (Apify)
### 4.1 Keyword search scrape — `xmolodtsov/tiktok-search-scraper`
- **Actor:** `xmolodtsov/tiktok-search-scraper` — **full id, bukan turunan `APIFY_ACTOR_PREFIX`**, sama seperti tiga actor lain. Env `APIFY_SEARCH_ACTOR_TIKTOK`.
- **Run mode:** sync (`run-sync-get-dataset-items`).
- **Input yang benar-benar dikirim** (`searchInput` → `tiktokSearchInput`):
  ```json
  {
    "keywords": ["<keyword>", ...],
    "maxItems": <max_posts>
  }
  ```
  - `keywords` — bagi actor, `#tag` adalah hashtag query dan kata biasa adalah full-text query; keduanya lewat apa adanya, `#` tidak dibuang.
  - `maxItems` adalah **batas total, bukan ukuran halaman** — dan karena actor menagih per hasil yang dikembalikan, ini adalah **plafon biaya satu run**. 30 keyword × 30 post = 900 hasil = $0.27. Bandingkan `resultsPerPage` milik actor sebelumnya, yang cuma ukuran halaman dan tidak membatasi apa pun yang ditagih.
  - **Tidak ada field tanggal yang dikirim, karena tidak ada yang bisa dikirim.** Actor ini tidak punya input tanggal sama sekali. `expansion` juga tidak dikirim: pool `suggest`/`hashtag`/`music` ditagih sebagai hasil dan mengembalikan baris yang bukan post yang kita minta.
- **Output row:** satu row per post, metriknya **flat di atas** dan hanya dua yang bersarang — `id`, `title`, `views`, `likes`, `comments`, `shares`, `bookmarks`, `hashtags[]`, `uploadedAt` (unix detik), `uploadedAtFormatted` (ISO), `postPage`, `keyword`, plus objek `channel{id,name,username,avatar,verified,url,followers,following,videos,likes}` dan `video{width,height,ratio,duration,url,cover,thumbnail}`.
  - Row ini **butuh parser sendiri** (`parseTikTokItems`).
  - `channel.id` dan `id` keduanya **string** di 11 row yang diuji — tidak seperti `authorMeta.id` actor lama yang pernah datang sebagai number.
  - `hashtags` bisa berisi `null` (terverifikasi di row nyata). Tidak dipakai parser, tapi jangan menambahkannya sebagai `[]string` tanpa mengubah tipenya — satu `null` akan menjatuhkan seluruh row.
- **Tidak ada `errorCode` di actor ini.** 11 row nyata tidak punya key ber-`error` sama sekali, jadi parser cukup gagal keras pada row tanpa `id` — actor rusak tidak akan tersembunyi di balik jumlah yang masuk akal.
- **Media gratis, tanpa flag berbayar.** `video.cover` dan `video.thumbnail` adalah URL CDN TikTok biasa yang ada di **setiap** row. Keduanya dicek HTTP 200 (jpeg 62–144 KB); `video.url` (file video) membalas 403 tanpa cookie dan tidak dipakai. Ini menghapus seluruh kelas bug "post tanpa media" yang dimiliki actor sebelumnya, yang menyembunyikan cover gratisnya di balik `mediaUrls` yang kosong kecuali flag download berbayar dinyalakan.
- **Tanggal:** `uploadedAtFormatted` (ISO) adalah field utama, `uploadedAt` (unix) fallback-nya. Parser memakai `parseActorTime` (baca UTC). Row tanpa keduanya **tetap disimpan** dengan `published_at` NULL — tanggal tidak pernah dikarang.
- **Normalisasi → DB:** `id`→`Post.external_id`, `channel.username`→`author_handle`, `channel.id`→`author_id`, `title`→`text`, `video.cover`→`media_urls`, `uploadedAtFormatted`/`uploadedAt`→`Post.published_at` (migration `000016`), dan metrics: `likes`→`likes`, `comments`→`comments`, `shares`→`shares`, **`views`→`views`**, `bookmarks`→`collects`.
  - **`views` harus tetap `views`.** Snapshot writer baca `metricsInt("views", "viewCount", "playCount")`; key yang tidak dikenal siapa pun akan tersimpan lalu dibaca tidak oleh siapa pun — hasilnya view count nol pada post yang selebihnya terlihat benar. `TopPostsByMetric` mengurutkan kolom `metric_snapshot.views`, jadi post dengan key asing tidak akan pernah masuk peringkat.
  - **`bookmarks` disimpan sebagai `collects`.** Save-count TikTok diukur sama, hanya dinamai berbeda; me-rename key-nya akan membuat nilai itu yatim di setiap post yang sudah tersimpan.
  - `collects` disimpan di `post.metrics` tetapi **belum punya kolom snapshot maupun renderer** — nilainya ada, belum tampil. Menambahkannya butuh kolom baru, bukan rename.
  - `postPage` yang dikirim actor **tidak disimpan** (tidak ada kolomnya). Halaman batch menyusun ulang permalink dari `authorHandle` + `externalId`, sama seperti IG dan Threads.
- **Biaya — pay-per-event, $0.30 per 1000 hasil di tier FREE.** Itu 12× lebih murah dari $3.70 yang dibayar actor sebelumnya, dan hampir tidak ada add-on yang perlu dinonaktifkan: yang ditagih hanyalah hasil yang dikembalikan.
  - Contoh: 1 batch 5 keyword × 30 post = 150 hasil → **$0.045**. Actor sebelumnya membayar $0.555 untuk halaman yang sama dan 90%-nya dibuang ingestor.
- **⚠ Yield.window tidak membaik karena ganti actor.** Actor ini **tidak** menerima batas tanggal, persis seperti actor sebelumnya — jadi pola "ambil habis, buang yang di luar window" tetap berlaku dan hanya bisa dikendalikan lewat `maxItems` dan keyword yang lebih sempit.
  - Yang benar-benar membaik adalah **halaman yang dikembalikan lebih kecil**: pada keyword yang sama, `clockworks` mengembalikan 50 baris untuk 3 yang in-window, sedangkan actor ini mengembalikan 10 baris untuk 3 yang sama. Dikombinasikan dengan harga satuan yang 12× lebih murah, biaya per post yang benar-benar terpakai turun dari **$0.062 ke $0.001**.
  - `sortType: DATE_POSTED` **tidak dikirim**: docs mengklaimnya mengurutkan terbaru lebih dulu, tapi diukur pada 2026-09 ia mengembalikan kumpulan yang sama dan tidak terurut dengan `RELEVANCE`. Field yang terbaca seperti filter tetapi bukan filter adalah kegagalan diam-diam.
## 5. Live Brief (bila ada)
- **Sumber:** —
- **Cara ambil live views:** —
## 6. Risiko & Catatan Platform
- **Field name salah = diam-diam.** Actor menerima payload, jatuh ke default-nya sendiri, dan mengembalikan 200 dengan data yang salah. Semua field di §4.1 dipin di `runner_test.go` (`TestSearchInputTikTok*`) karena itu.
- **Tidak ada mode per-post sama sekali.** `Scrape post by URL` masih ⬜ di `../PLATFORM_MATRIX.md` §2.1, dan actor ini memang **tidak** punya mode URL — inputnya keyword saja, ditambah pool `expansion`. Tidak ada parser maupun gate-nya karena tidak ada jalur yang bisa di-gate. Kalau scraping satu permalink dibutuhkan, itu actor terpisah, bukan flag di sini.
