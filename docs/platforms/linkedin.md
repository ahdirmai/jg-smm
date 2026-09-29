# Platform Brief — LinkedIn

> **Platform:** LinkedIn (`LINKEDIN`) · **Status:** deferred (slot siap) · **Brief versi:** `v0.0` · **Update:** —
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

- **Actor:** `harvestapi/linkedin-post-search` (Apify id `buIWk2uOUzTmcLsuB`) — **full id, bukan turunan `APIFY_ACTOR_PREFIX`**. Env `APIFY_SEARCH_ACTOR_LINKEDIN`. Dipakai lewat `linkedInSearchInput` (`runner.go:219`).
  - **Actor termurah di store justru yang salah.** `worldunboxer/rapid-linkedin-scraper` ($0.0005, 4× lebih murah dari yang kita pakai, dan **paling murah untuk LinkedIn sama sekali**) adalah scraper **lowongan kerja**, bukan post — inputnya `jobs_titles`, `cities`, `companies`, `employment_level`, `experience_level`, dan **tidak punya pencarian post sama sekali**. Dicatat di sini supaya orang berikutnya yang sedang memangkas biaya tidak memilihnya: termurah ≠ benar. Tolakannya sudah ada di `main.go:611`.
  - **Tidak ada mode per-post/URL.** `Scrape post by URL` masih ⬜ di `../PLATFORM_MATRIX.md` §2.1, dan actor ini memang **tidak punya input URL sama sekali** — satu-satunya jalurnya keyword. Mengambil satu permalink butuh actor terpisah, bukan flag di sini; tidak ada parser maupun gate-nya karena tidak ada jalur yang bisa di-gate.
- **Input:**
  ```json
  {
    "searchQueries": ["<keyword>", ...],
    "maxPosts": <max_posts>,
    "sortBy": "date",
    "postedLimitDate": "YYYY-MM-DD"
  }
  ```
  - `searchQueries` — **array, dan wajib**; actor ini tidak punya key `keywords` seperti empat actor lain. Satu string yang salah nama ditolak, bukan diabaikan.
  - **`maxPosts` wajib dikirim — ini plafon biaya satu run, dan mengosongkannya adalah kesalahan termahal di file ini.** `maxPosts` adalah batas **per query** yang sungguhan; actor punya default sendiri **100 post per query** dan menagih semuanya. Terukur pada 2026-09-29: batch yang meminta 5 post tapi mengirim `maxItems` (key yang **tidak ada** di actor ini, jadi dibaca mode diam-diam) mengembalikan **97 baris dan ditagih $0.096**. `runner_test.go:265` memverifikasi `maxPosts`, bukan `maxItems`, karena itu.
  - `sortBy: "date"` — terbaru dulu. Alasannya biaya, bukan estetika: relevance sort bisa memenuhi cap dengan post lama yang dibuang ingestor.
  - `postedLimitDate` — **bound bawah absolut sungguhan, di sisi server.** Inilah yang menyempitkan pool 97 baris terukur menjadi 1 baris: jendela sebagian besar dibayar oleh actor, bukan oleh ingestor. LinkedIn satu-satunya dari ketujuh platform yang bisa menggeser window ke dalam payload.
  - `postedLimit` (enum relatif: `1h`/`24h`/`week`/`month`/`3months`/`6months`/`year`) **tidak dikirim**: mengirim keduanya membiarkan actor memilih sendiri, dan yang kita pegang adalah batas absolut yang cocok dengan window yang benar-benar kita jalankan. Input lain yang ada tapi tidak dipakai: `scrapePages`, `startPage`.
  - `scrapeReactions` dan `scrapeComments` default `false` dan **tetap mati**. Keduanya ditagih sebagai **post tambahan**, jadi menyalakannya menambah tagihan tanpa menambah post yang disimpan — mode ini tidak mengambil comment body sama sekali.
- **Output field:** satu row per post, **22 key** di puncak: `id`, `entityId`, `type`, `content`, `contentAttributes`, `author`, `postedAt`, `postImages`, `engagement`, `socialContent`, `linkedinUrl`, `shareUrn`, `shareLinkedinUrl`, `reactionIds`, `commentIds`, `reactions`, `comments`, `article`, `newsletterTitle`, `newsletterUrl`, `query`, `header`. Hampir semuanya **bersarang** atau array — kelas wire shape yang tidak bisa dijembatani unmarshal case-insensitive Go, jadi baris ini butuh parser sendiri (`parseLinkedInItems`).
  - `id` dan `entityId` adalah **string yang identik** pada row nyata; parser memakai `id` dan jatuh ke `entityId`. Row tanpa keduanya itu error, bukan drop diam-diam — actor rusak tidak boleh tersembunyi di balik jumlah yang masuk akal. Row `type` selain `"post"` menghasilkan nol item, bukan error.
  - `content` sudah **dipotong actor** dengan "…" pada post panjang; yang tersimpan adalah yang actor kirim, bukan teks penuh.
  - `postedAt` bersarang: `date` (RFC3339 ber-`Z`) adalah field utama, `timestamp` adalah **MILLIS** dari momen yang sama, dan hanya itu yang dipakai sebagai fallback. Dua field `postedAgo*` ("20h", "20 hours ago • Edited • …") **tidak di-parse** — string relatif tidak bisa diubah jadi tanggal tanpa mengarang, dan field absolut di sebelahnya persis.
  - `engagement` bersarang: `{id, likes, comments, shares, reactions[]}`. **`reactions[]` bukan metrik** — itu rincian per tipe dari `likes` yang sama (LIKE 15 + EMPATHY 1 = `likes` 16). Disimpan sebagai metrik sendiri berarti engagement post terhitung dua kali dengan key yang tidak dimiliki platform lain; dibaca sebagai like count berarti kehilangan satu tipe yang bukan LIKE. Dipin di `TestParseLinkedInReactionsBreakdownIsNotAMetric`.
  - `author` bersarang: `id` (member id opaque), `publicIdentifier` (vanity handle), `name`, `type`, `linkedinUrl`. **`universalName` NULL di row nyata** — jadi ketiga field id dibaca lewat `anyToString`: mismatch tipe di *satu* key gagal membatalkan seluruh row, dan harga kegagalan itu teks, metrik, dan tanggal sebuah post.
  - `postImages` adalah **array** (`[]` pada row yang tertangkap, jadi bentuk media belum terverifikasi dari run nyata). Parser menerima beberapa ejaan objek media lalu jatuh ke pencarian URL rekursif berbatas kedalaman, supaya gambar ber-key lain tetap jadi URL dan tidak diam-diam berubah jadi "post ini tanpa media". Hasilnya selalu array non-nil: `media_urls` disimpan dan disajikan sebagai JSON, di mana `null` dan `[]` berbeda bagi semua pembaca hilir.
  - `socialContent` adalah **flag visibilitas** (`hideReactionsCount` dan sejenisnya). Tidak dipetakan apa pun: saat LinkedIn menyembunyikan counter, actor melaporkan `0`, dan `0` yang disembunyikan tidak bisa dibedakan dari `0` yang asli — nilai jujur adalah angka yang diberikan, bukan tebakan mana yang tersembunyi. Yang tidak boleh terjadi adalah flag yang membuat post hilang, atau count tersembunyi berubah jadi angka fabrikasi.
  - **Tidak ada view count di mana pun pada row ini**, dan tidak ada flag `socialContent` yang menyembunyikan view yang masih bisa kita ukur.
- **Normalisasi → DB:** `id`→`Post.external_id` (identik dengan `entityId`), `author.publicIdentifier`→`author_handle` (fallback `author.id`, lalu `"unknown"` untuk halaman perusahaan), `author.id`→`author_id`, `content`→`text`, `postImages[]`→`media_urls` (selalu non-nil), `postedAt.date` lalu `postedAt.timestamp`/1000→`Post.published_at`, dan metrics: `engagement.likes`→`likes`, `engagement.comments`→`comments`, `engagement.shares`→`shares`.
  - **Nama key harus persis kanonik.** Snapshot writer baca `metricsInt("likes","likeCount")`, `metricsInt("comments","commentCount")`, `metricsInt("shares","shareCount","retweetCount")`; key yang tidak dikenal siapa pun akan tersimpan lalu dibaca tidak oleh siapa pun.
  - **Tidak ada key `views` di jalur mana pun.** LinkedIn tidak mempublikasikan view count, dan `views: 0` yang tersimpan akan dibaca snapshot writer sebagai **pengukuran nyata** — setiap post LinkedIn akan peringkat terakhir di belakang post yang view-nya benar-benar nol. Absen adalah jawaban yang jujur, dan `metricsInt` sudah memperlakukannya sebagai 0. Dipin di `TestParseLinkedInInventsNoViewCount`.
  - `linkedinUrl`/`shareLinkedinUrl` **tidak disimpan** — tidak ada kolom permalink. Halaman dashboard menyusun ulang URL post dari `authorHandle` + `externalId`, sama seperti IG, Threads, dan TikTok.
  - Kehadiran metrik ditentukan **pointer presence, bukan nilai non-nol**: post dengan 0 komentar memang punya count 0 dan actor mengatakannya. Hanya field yang benar-benar tidak ada di row yang dibiarkan kosong — `0` terukur dan `0` yang tidak dilaporkan bukan hal yang sama.
  - **Biaya — $0.002 per post di tier FREE** (terbayar, bukan angka halaman store: headline "$1.50 / 1000" di store adalah tier yang berbeda). Tidak ada event per QUERY seperti YouTube dan X, jadi unlike kedua platform itu, harga LinkedIn murni per hasil — **dan `maxPosts` adalah satu-satunya remnya.** Tanpa rem itu, default 100 post per query = $0.20 per keyword sebelum satu baris pun dilihat.

## 5. Live Brief (bila ada)

- **Sumber:** `<...>`
- **Cara ambil live views:** `<...>`

## 6. Risiko & Catatan Platform

- LinkedIn: post, comment, reaction; kebijakan ketat untuk automation. Belum ada brief.
- Belum diaktifkan. Brief ditulis saat rollout R3.
- Kebijakan platform: `<...>`
- Sinyal ban / rate limit khusus: `<...>`
- Perbedaan penting vs platform lain: `<...>`

## 7. Riwayat Brief

| Versi | Tanggal | Perubahan |
| --- | --- | --- |
| v0.0 | | Stub dibuat |
