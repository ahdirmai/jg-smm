# Platform Matrix & Rollout — MVP-1-SMM

> **Tujuan:** satu sumber kebenaran untuk **7 platform** yang akan ditangani sistem (Threads, Facebook, Instagram, LinkedIn, X, YouTube, TikTok) — tanpa menduplikasi desain per platform (DRY). Dokumen ini menetapkan: kapabilitas per platform, kontrak adapter, strategi rollout, dan status brief.
>
> **Keputusan terkunci:** MVP aktif = **Instagram + Threads**. 5 platform lain = slot adapter siap; brief detail diisi bertahap di `platforms/<nama>.md`.
>
> **Prinsip:** semua **action via Playwright** (browser automation headful) — **tidak** ada OAuth/API untuk action. **Login juga via Playwright** (headful, non-OAuth) untuk **semua** platform. Scrape = Apify (per platform actor).
>
> **Konfirmasi (locked):** (1) brief action per platform ditulis di `platforms/<slug>.md` (format `_TEMPLATE.md`, §5); (2) login = Playwright untuk semua platform.

---

## 1. Daftar Platform (Target Akhir)

| Kode | Platform | Status MVP | Slug brief |
| --- | --- | --- | --- |
| `INSTAGRAM` | Instagram | **AKTIF** | `platforms/instagram.md` |
| `THREADS` | Threads | **AKTIF** | `platforms/threads.md` |
| `FACEBOOK` | Facebook | deferred (slot siap) | `platforms/facebook.md` |
| `X` | X (Twitter) | deferred (slot siap) | `platforms/x.md` |
| `LINKEDIN` | LinkedIn | deferred (slot siap) | `platforms/linkedin.md` |
| `YOUTUBE` | YouTube | deferred (slot siap) | `platforms/youtube.md` |
| `TIKTOK` | TikTok | deferred (slot siap) | `platforms/tiktok.md` |

Enum `Platform` di `ERD.md` sudah memuat ke-7 nilai ini — **tidak ada** migrasi enum saat menambah platform.

---

## 2. Platform Capability Matrix

Legend: **✅** didukung/diimplementasi · **🟡** brief-tbd (desain konsep ada, detail ditulis nanti) · **⬜** tertunda (slot siap) · **❌** N/A / tidak mungkin via Playwright. **Semua action = Playwright.**

### 2.1 Scrape (via Apify actor per platform)

| Kapabilitas | IG | Threads | FB | X | LinkedIn | YouTube | TikTok |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Scrape post by URL | ✅ | ✅ | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| Scrape profile/handle | ✅ | ✅ | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| Scrape hashtag | ✅ | 🟡 | ⬜ | ⬜ | ⬜ | 🟡 | ⬜ |
| Scrape mention | ✅ | ✅ | ⬜ | ⬜ | ⬜ | 🟡 | ⬜ |
| Scrape comments | ✅ | ✅ | ⬜ | ⬜ | ⬜ | 🟡 | ⬜ |
| Metric: reach | ✅ | 🟡 | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| Metric: views (video/reels) | ✅ | 🟡 | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| Live views | 🟡 | ❌ | ⬜ | ❌ | ❌ | 🟡 | 🟡 |

### 2.2 Action (semua via Playwright)

Action yang diminta: **Comment Posting, Reply Comment, Like, Comment, Share, Repost, Like Comment, Reply Comment, Report** + pemantauan **Views Posting / Reach / Live**.

| Action | IG | Threads | FB | X | LinkedIn | YouTube | TikTok |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Comment on post | ✅ | ✅ | 🟡 | 🟡 | 🟡 | 🟡 | 🟡 |
| Reply comment | ✅ | ✅ | 🟡 | 🟡 | 🟡 | 🟡 | 🟡 |
| Like post | ✅ | ✅ | 🟡 | 🟡 | 🟡 | 🟡 | 🟡 |
| Like comment | ✅ | ✅ | 🟡 | 🟡 | 🟡 | 🟡 | 🟡 |
| Report post | 🟡 | 🟡 | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| Report comment | 🟡 | 🟡 | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| Share | ⬜ | 🟡 | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| Repost | ❌ | 🟡 | ⬜ | 🟡 | 🟡 | ❌ | 🟡 |
| Views (read/harvest) | ✅ | 🟡 | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |

> **Catatan penting:** sel **🟡/⬜** = brief per-platform **wajib dikonfirmasi** sebelum implementasi (alur UI, selector, aturan anti-spam platform, verifikasi ground truth). Nilai di atas adalah **dugaan awal**, bukan komitmen. Brief per platform ditulis di `platforms/<slug>.md` (format §5, disetujui user). Setiap platform punya kebijakan berbeda — mis. LinkedIn/X lebih ketat & berisiko tinggi untuk automation; YouTube comment butuh state channel tertentu.
>
> **Action brief berbeda tiap platform** (diinginkan user). Kontrak adapter **sama**; isinya (alur + selector + verifikasi) **berbeda** dan ditulis di brief masing-masing.

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

| Fase | Platform baru | Prasyarat | Deliverable |
| --- | --- | --- | --- |
| MVP (P2–P3) | — (IG + Threads) | — | IG & Threads penuh |
| R1 | **Facebook** | brief FB + actor + adapter + test | scrape + action dasar FB |
| R2 | **X** | brief X + adapter (hati-hati risiko ban) | scrape + action dasar X |
| R3 | **LinkedIn** | brief LI + adapter | scrape + action dasar LI |
| R4 | **YouTube** | brief YT (comment butuh channel state) | scrape + comment |
| R5 | **TikTok** | brief TT + live monitoring | scrape + action dasar TT + live |

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
