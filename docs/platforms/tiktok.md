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

Action **tidak** dikerjakan di MVP ini. Yang hidup hanya keyword scrape (§4.1). `profiles[]`, `postURLs[]`, dan `hashtags[]` milik actor **tidak** diintegrasikan — parser di §4.1 bisa dipakai ulang untuk `hashtags[]` tanpa perubahan bentuk row.
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
### 4.1 Keyword search scrape — `clockworks/tiktok-scraper`
- **Actor:** `clockworks/tiktok-scraper`, id lengkap `GdWCkxBtKWOsKjdch` — **full id, bukan turunan `APIFY_ACTOR_PREFIX`**. Env `APIFY_SEARCH_ACTOR_TIKTOK`.
- **Run mode:** sync (`run-sync-get-dataset-items`).
- **Input yang benar-benar dikirim** (`searchInput` → `tiktokSearchInput`):
  ```json
  {
    "searchQueries": ["<keyword>", ...],
    "profileSorting": "latest",
    "resultsPerPage": <max_posts>,
    "commentsPerPost": 0,
    "oldestPostDateUnified": "YYYY-MM-DD",
    "newestPostDate": "YYYY-MM-DD"
  }
  ```
  - `searchQueries` — **bukan** `keywords` dan **bukan** `hashtags[]`. Bagi actor, `#tag` adalah hashtag query dan kata biasa adalah full-text query; keduanya lewat apa adanya, `#` tidak dibuang.
  - `profileSorting: "latest"` — **wajib, bukan preferensi.** Actor hanya menghormati `oldestPostDateUnified`/`newestPostDate` pada mode `latest`/`oldest`. Di `popular` field tanggal **diterima lalu diabaikan**, run tetap sukses, dan hasilnya video terbaru-terpopuler lintas waktu. Ingestor tetap menerapkan `inWindow` sebagai jaring pengaman — itu satu-satunya baris pertahanan yang tidak bisa dimatikan field payload.
  - `resultsPerPage` adalah **ukuran halaman, bukan batas total**. 500 post pada `resultsPerPage: 100` tetap ditagih 500 hasil. Tidak ada field payload yang membatasi tagihan; yang membatasi adalah `commentsPerPost`.
- **Output row:** satu row per post, bentuknya **nested** — `id`, `text`, `createTimeISO`, `createTime` (unix detik), `webVideoUrl`, `mediaUrls[]`, `diggCount`, `commentCount`, `shareCount`, `collectCount`, `playCount`, `hashtags[]`, `isAd`, plus objek `authorMeta{id,name,nickName,signature,fans,heart,verified}`, `videoMeta{duration,coverUrl,format,width,height}`, `musicMeta{id,musicName,authorName,original}`.
  - Row ini **butuh parser sendiri** (`parseTikTokItems`). Bedanya dari row IG flat: tidak ada jembatan underscore↔camel, bentuknya memang berbeda.
  - `authorMeta.id` datang sebagai **number**. Field bertipe `string` akan gagal unmarshal **seluruh row** dan menjatuhkan run — dipakai `any` + `anyToString`, seperti parser IG.
- **`errorCode` — row error berinterleave dengan hasil asli.** Actor menyatakan item normal tidak pernah terpengaruh, jadi `errorCode` satu-satunya cara membedakan keduanya dan **wajib dibaca sebelum guard external-id**: row error tidak punya `id`, sehingga guard itu menyala dan error-nya merambat ke seluruh ingest — beserta row-row valid di dataset yang sama. Kode yang emitted: `PROFILE_PRIVATE`, `SEARCH_QUERY_NOT_FOUND`, `INVALID_INPUT`, `FILTER_NO_PASS`, `PASSWORD_RESET`, `UNKNOWN`. `INVALID_INPUT` juga menghentikan run.
  - Row yang **tidak** punya `id` **dan** tidak punya `errorCode` tetap dianggap error, bukan di-drop diam-diam — Kalau di-drop, actor rusak tersembunyi di balik jumlah yang masuk akal.
- **Tanggal:** `createTimeISO` sering tiba **tanpa offset** (`2026-08-03T07:06:51`), yang ditolak RFC3339. Parser memakai `parseActorTime` (baca UTC), dengan fallback ke `createTime` unix untuk row yang ISO-nya kosong/tidak bisa dibaca. Row tanpa kedua field **tetap disimpan** dengan `published_at` NULL — tanggal tidak pernah dikarang.
- **Normalisasi → DB:** `id`→`Post.external_id`, `authorMeta.name`→`author_handle`, `authorMeta.id`→`author_id`, `text`→`text`, `mediaUrls[]`→`media_urls`, `createTimeISO`/`createTime`→`Post.published_at` (migration `000016`), `diggCount`/`commentCount`/`shareCount`/`playCount`/`collectCount`→`Post.metrics` + `MetricSnapshot` (kunci `likes`/`comments`/`shares`/`plays`/`collects` — kosakata lintas-platform yang sudah dibaca `metricsLine`).
- **⚠ Tidak mengambil comment bodies.** Actor **`GdWCkxBtKWOsKjdch` storing comment-nya di dataset terpisah**, yang URL-nya ada di field `commentsDatasetURL` pada item normal; take it secara manual (curl/axios). Integrasi ini **tidak** mengambil URL itu, jadi `commentsPerPost: 0` — bukan hanya supaya tidak ada yang terbuang, tapi supaya tidak ada yang **ditagih**. Batas yang sama seperti jalur keyword Instagram (`instagram.md` §4.2).
  > `ponytail:` keyword batch menampilkan post, bukan thread komentar. Kalau comment bodies dibutuhkan, tambahkan pembaca dataset kedua di `Runner` (`GET commentsDatasetURL` → `storeItems` dengan key `apify/search-<runID>/comments-N.json`) dan set `commentsPerPost` > 0 — biayanya per komentar, jadi_putuskan dulu.
- **Biaya — pay-per-event, mulai $1.70 per 1000 hasil.** Satu video = satu hasil. Add-on berikut **ditagih terpisah dan sengaja tidak dikirim** (bukan `false`, tapi absen, supaya edit berikutnya tidak menyalakannya diam-diam): `shouldDownloadVideos`, `shouldDownloadMusic`, `shouldDownloadSubtitles`, `shouldDownloadCoverImages`, `shouldDownloadProfilePhotos`, `downloadSummarizedVideoDescription`, `downloadAIVideoSummaries`, `downloadSubtitlesOptions`. Yang AI **ditagih per detik video** — jauh lebih besar dari tagihan per hasil.
  - Contoh hitungan: 1 batch 5 keyword × 30 post = 150 hasil → 150/1000 × $1.70 = **$0.255**.
- **⚠ Yield.window.** Berbeda dari actor IG, actor ini **menerima** batas tanggal, jadi window dikirim ke actor. Tapi `profileSorting` yang mengaktifkannya — kalau sort berubah, yield anjlok ke pola yang sama seperti IG: item di-fetch habis lalu dibuang ingestor. Run pembanding `d3fc79a8` (IG): 150 diambil, 27 disimpan — **18%**. Ukur ulang yield setelah run TikTok pertama sebelum mengklaim apa pun soal perbaikan.
## 5. Live Brief (bila ada)
- **Sumber:** —
- **Cara ambil live views:** —
## 6. Risiko & Catatan Platform
- **Batas tanggal ganda.** Store listing menyebut **dua** mekanisme tanggal — `oldestPostDateUnified`/`newestPostDate` (untuk profil/hashtag) dan `videoSearchDateFilter` (bersama `videoSearchSorting`, untuk video search) — dan **tidak menyatakan yang mana yang governs `searchQueries`**. Test payload hanya membuktikan apa yang kita kirim, bukan bahwa actor memperlakukannya. Satu-satunya bukti adalah run nyata: bila ada `published_at` di luar window, tambahkan `videoSearchDateFilter` lalu jalankan ulang.
- **Field name salah = diam-diam.** Actor menerima payload, jatuh ke default-nya sendiri, dan mengembalikan 200 dengan data yang salah. Semua field di §4.1 dipin di `runner_test.go` (`TestSearchInputTikTok*`) karena itu.
- **Tidak ada mode per-post.** `Scrape post by URL` masih ⬜ di `../PLATFORM_MATRIX.md` §2.1; actor mendukungnya (`postURLs[]`) tapi belum ada parser maupun gate-nya.
