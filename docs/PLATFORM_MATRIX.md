# Platform Matrix & Rollout — MVP-1-SMM

> **Tujuan:** satu sumber kebenaran untuk **7 platform** yang akan ditangani sistem (Threads, Facebook, Instagram, LinkedIn, X, YouTube, TikTok) — tanpa menduplikasi desain per platform (DRY). Dokumen ini menetapkan: kapabilitas per platform, kontrak adapter, strategi rollout, dan status brief.
>
> **Keputusan terkunci:** MVP aktif = **Instagram + Threads**. 5 platform lain = slot adapter siap; brief detail diisi bertahap di `platforms/<nama>.md`.
>
> **Prinsip:** semua **action via Playwright** (browser automation headful) — **tidak** ada OAuth/API untuk action. **Login juga via Playwright** (headful, non-OAuth) untuk **semua** platform. Scrape = Apify (per platform actor).
>
> **Konfirmasi (locked):** (1) brief action per platform ditulis di `platforms/<slug>.md` (format `platforms/_TEMPLATE.md`, §5); (2) login = Playwright untuk semua platform.

---

## 1. Daftar Platform (Target Akhir)

| Kode        | Platform    | Status MVP           | Slug brief               |
| ----------- | ----------- | -------------------- | ------------------------ |
| `INSTAGRAM` | Instagram   | **AKTIF**            | `platforms/instagram.md` |
| `THREADS`   | Threads     | **AKTIF**            | `platforms/threads.md`   |
| `FACEBOOK`  | Facebook    | deferred (slot siap) | `platforms/facebook.md`  |
| `X`         | X (Twitter) | deferred (slot siap) | `platforms/x.md`         |
| `LINKEDIN`  | LinkedIn    | deferred (slot siap) | `platforms/linkedin.md`  |
| `YOUTUBE`   | YouTube     | deferred (slot siap) | `platforms/youtube.md`   |
| `TIKTOK`    | TikTok      | **AKTIF** (keyword)   | `platforms/tiktok.md`    |

Enum `Platform` di `ERD.md` sudah memuat ke-7 nilai ini — **tidak ada** migrasi enum saat menambah platform.

---

## 2. Platform Capability Matrix

Legend: **✅** didukung/diimplementasi · **🟡** brief-tbd (desain konsep ada, detail ditulis nanti) · **⬜** tertunda (slot siap) · **❌** N/A / tidak mungkin via Playwright. **Semua action = Playwright.**

### 2.1 Scrape (via Apify actor per platform)

| Kapabilitas                 | IG  | Threads | FB  | X   | LinkedIn | YouTube | TikTok |
| --------------------------- | --- | ------- | --- | --- | -------- | ------- | ------ |
| Scrape post by URL          | ✅  | ✅      | ⬜  | ⬜  | ⬜       | ⬜      | ⬜     |
| Scrape profile/handle       | ✅  | ✅      | ⬜  | ⬜  | ⬜       | ⬜      | ⬜     |
| Scrape hashtag              | ✅  | 🟡      | ⬜  | ⬜  | ⬜       | 🟡      | ⬜     |
| Scrape mention              | ✅  | ✅      | ⬜  | ⬜  | ⬜       | 🟡      | ⬜     |
| Scrape comments             | ✅  | ✅      | ⬜  | ⬜  | ⬜       | 🟡      | ⬜     |
| **Scrape keyword batch**     | ✅  | ✅      | ✅  | ✅  | ✅       | ✅      | ✅     |
| Metric: reach               | ✅  | 🟡      | ⬜  | ⬜  | ⬜       | ⬜      | ⬜     |
| Metric: views (video/reels) | ✅  | 🟡      | ⬜  | ✅  | ⬜       | ✅      | ✅     |
| Live views                  | 🟡  | ❌      | ⬜  | ❌  | ❌       | 🟡      | 🟡     |

Ketujuh platform sekarang menjalankan keyword batch, tetapi sifat biaya dan
cakupan filter-nya berbeda satu sama lain — "keyword batch ✅" bukan berarti
fitur yang sama:

| Platform | Actor | $/hasil | Filter tanggal server-side? | Batas hasil? |
| --- | --- | --- | --- | --- |
| YouTube | `apidojo/youtube-scraper-api` | $0.0005 **+ ~$0.015 per query** | Ya — `uploadDate` (enum bucket) | `maxItems` |
| LinkedIn | `harvestapi/linkedin-post-search` | $0.002 | Ya — `postedLimitDate` (absolut) | `maxPosts` (wajib) |
| X | `apidojo/twitter-scraper-lite` | $0.0004 **+ ~$0.016 per query** | **Tidak ada** | **Tidak ada** |
| Facebook | `danek/facebook-search-ppr` | ~$0.00001 | Tidak ada | `max_posts` |

Dua konsekuensi yang harus dibaca sebelum menjalankan batch luas:

1. **YouTube dan X menagih per QUERY, di luar biaya per hasil.** Satu keyword
   sudah ~$0.015–0.016 sebelum satu baris pun datang. Batch 5 keyword ≈ $0.08
   hanya dari tarif query, dan angka itu tidak terlihat di halaman store.
2. **X tidak punya batas hasil maupun filter tanggal sama sekali.** Tidak ada
   field payload yang bisa dipersempit, jadi satu batch X dapat mengembalikan
   pool penuh dan membakar sisa kredit bulan ini. Perlakukan sebagai platform
   paling berbahaya secara biaya, bukan yang paling murah.

Pemilihan actor membatalkan asumsi "paling mahal = paling benar". Untuk keempat
platform ini actor termurah di store justru yang salah: `worldunboxer/rapid-linkedin-scraper`
($0.0005, termurah untuk LinkedIn) adalah scraper **lowongan kerja**, bukan post
(`jobs_titles`/`cities`/`employment_level`), dan `apify/facebook-posts-scraper`
(115 ribu user, paling terbukti) hanya menerima **URL halaman**, bukan keyword,
jadi tidak bisa melayani keyword batch sama sekali.

Baris **Scrape keyword batch** adalah satu-satunya yang TikTok penuhi di v0.1: `xmolodtsov/tiktok-search-scraper` via `keywords[]`. Actor ini **tidak punya input tanggal sama sekali**, jadi window 100% ditegakkan ingestor — tidak ada field payload yang bisa dipersempit. `Metric: views` terisi karena `views` dipetakan ke `Post.metrics.views` — **bukan** key baru: snapshot writer hanya membaca `views`/`viewCount`/`playCount`, dan `TopPostsByMetric` mengurutkan kolom `metric_snapshot.views`, jadi key yang tak dikenal membuat setiap post TikTok tersimpan dengan view count nol dan tidak pernah masuk peringkat. `bookmarks` disimpan sebagai `collects` di `post.metrics` tapi belum punya kolom snapshot maupun renderer. Row tanpa `id` dianggap error, bukan di-drop diam-diam.

### 2.2 Action (semua via Playwright)

Action yang diminta: **Comment Posting, Reply Comment, Like, Comment, Share, Repost, Like Comment, Reply Comment, Report** + pemantauan **Views Posting / Reach / Live**.

| Action               | IG  | Threads | FB  | X   | LinkedIn | YouTube | TikTok |
| -------------------- | --- | ------- | --- | --- | -------- | ------- | ------ |
| Comment on post      | ✅  | ✅      | 🟡  | 🟡  | 🟡       | 🟡      | 🟡     |
| Reply comment        | ✅  | ✅      | 🟡  | 🟡  | 🟡       | 🟡      | 🟡     |
| Like post            | ✅  | ✅      | 🟡  | 🟡  | 🟡       | 🟡      | 🟡     |
| Like comment         | ✅  | ✅      | 🟡  | 🟡  | 🟡       | 🟡      | 🟡     |
| Report post          | 🟡  | 🟡      | ⬜  | ⬜  | ⬜       | ⬜      | ⬜     |
| Report comment       | 🟡  | 🟡      | ⬜  | ⬜  | ⬜       | ⬜      | ⬜     |
| Share                | ⬜  | 🟡      | ⬜  | ⬜  | ⬜       | ⬜      | ⬜     |
| Repost               | ❌  | 🟡      | ⬜  | 🟡  | 🟡       | ❌      | 🟡     |
| Views (read/harvest) | ✅  | 🟡      | ⬜  | ⬜  | ⬜       | ⬜      | ⬜     |

> **Catatan penting:** sel **🟡/⬜** = brief per-platform **wajib dikonfirmasi** sebelum implementasi (alur UI, selector, aturan anti-spam platform, verifikasi ground truth). Nilai di atas adalah **dugaan awal**, bukan komitmen. Brief per platform ditulis di `platforms/<slug>.md` (format §5, disetujui user). Setiap platform punya kebijakan berbeda — mis. LinkedIn/X lebih ketat & berisiko tinggi untuk automation; YouTube comment butuh state channel tertentu.
>
> **Action brief berbeda tiap platform** (diinginkan user). Kontrak adapter **sama**; isinya (alur + selector + verifikasi) **berbeda** dan ditulis di brief masing-masing.

---

### 2.3 Monitoring Akun Resmi (Official Accounts · 3rd-party analytics)

> **Bukan worker.** Akun resmi = akun brand/client yang **dipantau** (read-only, tanpa login/action).
> Semua metrik di bawah datang dari **3rd-party provider** (bukan self-scrape, bukan Playwright),
> melalui adapter provider tunggal (lihat `SYSTEM_DESIGN.md` → Analytics). Setiap platform punya
> **halaman analytics sendiri**; KPI-nya berbeda-beda.

Legend: **✅** tersedia di provider (target MVP bila platform aktif) · **🟡** kemungkinan tersedia, perlu konfirmasi provider · **⬜** belum/tidak untuk MVP.

| Metrik                               | IG  | Threads | FB  | X   | LinkedIn | YouTube | TikTok |
| ------------------------------------ | --- | ------- | --- | --- | -------- | ------- | ------ |
| Followers                            | ✅  | ✅      | 🟡  | 🟡  | 🟡       | ✅      | ✅     |
| Reach                                | ✅  | ✅      | 🟡  | 🟡  | 🟡       | 🟡      | 🟡     |
| Views (post/video/reels)             | ✅  | ✅      | 🟡  | 🟡  | 🟡       | ✅      | ✅     |
| Profile views                        | ✅  | 🟡      | 🟡  | 🟡  | 🟡       | ⬜      | 🟡     |
| Engagements (likes+comments+shares)  | ✅  | ✅      | 🟡  | 🟡  | 🟡       | ✅      | ✅     |
| Mentions                             | ✅  | ✅      | 🟡  | 🟡  | 🟡       | 🟡      | 🟡     |
| Followers delta (growth)             | ✅  | ✅      | 🟡  | 🟡  | 🟡       | ✅      | ✅     |
| Post-level breakdown                 | ✅  | 🟡      | 🟡  | 🟡  | 🟡       | 🟡      | ❌     |
| Watch time                           | ❌  | ❌      | ⬜  | ⬜  | ⬜       | ✅      | 🟡     |
| Live viewers (live TikTok / live IG) | 🟡  | ❌      | ⬜  | ❌  | ❌       | 🟡      | 🟡     |
| Sentiment (mention)                  | 🟡  | 🟡      | ⬜  | 🟡  | ⬜       | ⬜      | ⬜     |
| Demographics (age/gender/geo)        | 🟡  | ⬜      | 🟡  | ⬜  | 🟡       | 🟡      | 🟡     |
| Best posting time                    | 🟡  | 🟡      | 🟡  | 🟡  | 🟡       | 🟡      | 🟡     |

**Rincian engagement + metrik per konten.** Satu halaman analytics menampilkan
strip KPI, tren, dan dua tabel turunan:

- **Engagement breakdown** (`GET /api/analytics/{platform}` → `breakdown`):
  `likes`, `comments`, `saves`, `shares`, `accounts_engaged` dipecah dari
  `analytics_snapshot.metrics` (blob JSON), plus `engagementRatePct` =
  interactions ÷ reach. Bagian yang tidak dilaporkan provider tetap `null` →
  `—`, bukan `0`.
- **Metrik per konten** (`GET /api/analytics/{platform}/content`):
  satu baris per post (caption, permalink, tipe, tanggal publikasi, reach /
  views / interactions / likes / comments / saves / shares / engagement rate),
  diurutkan reach. Ditulis tiap ingest ke `analytics_content_metric`, **10
  post per akun per pass**.

> Metric insights media Instagram bersifat **lifetime** (running total sejak
> publikasi), jadi tabelnya adalah *gauge* yang di-refresh in-place lewat upsert
> pada `(official_account_id, external_id, provider)` — **bukan** time series,
> karenanya tidak memakai hypertable.
> Satu set metric berlaku untuk feed dan reels: `profile_visits`/`follows`
> bersifat akun-level dan meminta REELS untuk keduanya **gagal seluruh call**,
> `impressions` sudah deprecated Instagram, `replies` ditolak carousel.
> Kegagalan ambil metrik konten **menggagalkan run akun** (bukan diam-diam) —
> tabel yang berhenti refresh tidak bisa dibedakan dari tabel yang isinya
> semuanya sudah mati. Baris yang sudah tertulis tetap aman karena upsert
> di-commit sebelum error.
>
> **Hanya Instagram** yang terpasang. **TikTok terblokir dua kali**: tidak ada
> tool insights per konten di Composio, dan reach-nya sendiri tidak bisa
> diambil (lihat catatan di bawah) — jadi engagement rate TikTok mustahil
> dihitung meski tool-nya ada.

**KPI default per halaman analytics** (tiap halaman menampilkan strip KPI + tren):

Hanya enam skalar yang tersimpan di `analytics_snapshot`
(`followers, reach, views, engagements, mentions, profile_views`). Yang boleh
berbeda antar platform adalah **label** dan **urutan** — bukan key-nya. Key
yang tidak pernah diisi provider hanya menghasilkan kartu `—` selamanya.

| Halaman   | KPI utama                                          |
| --------- | -------------------------------------------------- |
| Instagram | Reach, Views, Interactions, Profile visits, Followers |
| Threads   | Views, Interactions, Profile views, Followers      |
| Facebook  | Reach, Page views, Post engagements, Page followers |
| LinkedIn  | Unique visitors, Engagements, Followers            |
| X         | Impressions, Engagements, Followers                |
| YouTube   | Views, Interactions, Subscribers                   |
| TikTok    | Video views, Interactions, Followers               |

> **Status data.** Setiap KPI dapat `lastFetchedAt` + badge `stale` bila > 60 menit.
> Provider = **Composio** (`ANALYTICS_PROVIDER=composio`, `COMPOSIO_API_KEY`).
> Akun resmi diikat ke Composio lewat `official_account.provider_ref` (`ca_...`).
> **Instagram saja** yang terpasang. Facebook menyusul
> (`FACEBOOK_GET_PAGE_INSIGHTS` tersedia). **TikTok tidak punya tool insights
> sama sekali di Composio** — hanya `TIKTOK_GET_USER_STATS` (followers/likes/
> video_count) — jadi reach TikTok tidak bisa diambil dari connector ini.
> `mentions` tidak terisi di platform mana pun: toolkit Instagram tidak
> menyediakan tool mentions sama sekali.

---

## 3. Kontrak Adapter (Interface Tunggal)

Invarian: menambah platform = **tambah file adapter + daftar di registry**, tanpa menyentuh controller/inti (Open/Closed Principle).

### 3.1 Scrape Adapter (BE, Apify)

```
interface ScrapeAdapter {
  Platform()                  // enum value
  BuildActorInput(target)     // { url | handle | hashtag | mention } -> actor input JSON
  Normalize(rawPayload)       // raw -> Post | Comment | MetricSnapshot
  Capabilities()              // mana yang didukung (dipakai UI untuk disable fitur)
}
```

Registry: `map[Platform]ScrapeAdapter` di `internal/adapter/apify/registry.go`.

### 3.2 Action Adapter (Worker, Playwright)

```
interface ActionAdapter {
  Platform()
  login(ctx, creds) -> outcome                 // headful; verified | needs_input | failed
  comment(ctx, job) -> result + verify()       // posting + ground-truth check
  replyComment(ctx, job) -> result + verify()
  like(ctx, job) -> result + verify()
  likeComment(ctx, job) -> result + verify()
  report(ctx, job) -> result                   // bila didukung
  share(ctx, job) -> result                    // bila didukung
  repost(ctx, job) -> result                   // bila didukung
  Capabilities()                               // action mana yang didukung platform ini
  SEL                                          // selector object (satu titik patch saat markup berubah)
}
```

Registry: `map[Platform]ActionAdapter` di `apps/worker/src/platforms/registry.js`.

### 3.3 Aturan Kontrak (berlaku semua platform)

- **Capabilities() wajib jujur** — BE & FE memakainya untuk menyembunyikan action yang tak didukung (jangan enqueue action ke platform yang tak bisa).
- **Verifikasi ground truth wajib** untuk action tulis (comment muncul di feed, like state berubah). Gagal verifikasi = `FAILED`, bukan `SUCCESS`.
- **Selector terpusat di `SEL`** per platform — markup berubah = satu titik patch.
- **Login = Playwright headful** (bukan OAuth/API). Fingerprint (UA/locale/viewport) fixed per container; proxy per akun (context-level).
- **Rate limit & jitter per platform** (dikonfigurasi di BE scheduler, konstanta SSOT) — tiap platform beda.

---

## 4. Rollout Plan (bertahap)

| Fase        | Platform baru    | Prasyarat                                | Deliverable                     |
| ----------- | ---------------- | ---------------------------------------- | ------------------------------- |
| MVP (P2–P3) | — (IG + Threads) | —                                        | IG & Threads penuh              |
| R1          | **Facebook**     | brief FB + actor + adapter + test        | scrape + action dasar FB        |
| R2          | **X**            | brief X + adapter (hati-hati risiko ban) | scrape + action dasar X         |
| R3          | **LinkedIn**     | brief LI + adapter                       | scrape + action dasar LI        |
| R4          | **YouTube**      | brief YT (comment butuh channel state)   | scrape + comment                |
| R5          | **TikTok**       | brief TT + live monitoring               | scrape + action dasar TT + live |

**Urutan alasan:** FB → X → LinkedIn (meta-ecosystem dulu, engagement tinggi), lalu YouTube/TikTok (kebutuhan khusus: video/live).

> Setiap platform baru **menambah** `MAX_ACCOUNTS_PER_CONTAINER` efektif (container bisa host 1 akun/platform); blast radius naik proporsional — lihat `INFRA_ANALYST.md` §6.

---

## 5. Template Brief Platform

Setiap `platforms/<slug>.md` memakai struktur ini (lihat `platforms/_TEMPLATE.md`):

1. **Metadata** — platform, status, versi, tanggal update.
2. **Kapabilitas** — tabel action/scrape didukung (sinkron §2 dokumen ini).
3. **Login flow** — halaman, step, sinyal sukses (cookie/elemen), penanganan 2FA/checkpoint.
4. **Action brief per action** — untuk tiap action yang didukung:
   - Alur UI (step-by-step) + entry point (URL/menu).
   - Selector (`SEL`): elemen kunci.
   - Verifikasi ground truth (cara memastikan berhasil).
   - Rate limit / jeda / risiko.
5. **Scrape brief** — actor input, field output, normalisasi → entitas DB.
6. **Live brief** (bila ada) — cara ambil live views.
7. **Risiko & catatan** — kebijakan platform, sinyal ban, khusus.
8. **Riwayat** — changelog brief.

---

## 6. Referensi

- `ERD.md` — enum `Platform` (7 nilai sudah ada).
- `SYSTEM_DESIGN.md` — komponen, transport, provisioning.
- `DEVELOPMENT_RULE.md` §7 — worker adapter & kontrak.
- `INFRA_ANALYST.md` §6 — blast radius saat platform bertambah.
- `platforms/` — brief per platform (template + 7 stub).
