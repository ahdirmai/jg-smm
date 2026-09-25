# Batch Scrape (Background) vs Post Scrape (Satuan)

> Update 2026-09-24 — keyword scrape dijadikan background batch; post scrape tetap satuan sync.

## Ringkasan

- **Post scrape (satuan)**: `POST /api/scrape/target` — 1 URL → 1 post + comments. Tetap **sync** (loader sampai Apify selesai, 10–30s). Dipakai di `/actions` enqueue flow.
- **Batch scrape (keyword)**: `POST /api/scrape/keywords` — 1..5 keywords + window → banyak post. Sekarang **async background**: return `202 { batch }` langsung (±10ms), proses Apify + ingest jalan di background. User lihat daftar batch di `/scrape`, klik batch masuk dashboard batch.

## Kenapa background

Keyword search = run-sync Apify bisa 30–90s (proxy RESIDENTIAL + banyak pages). Sync bikin browser timeout / 502 di reverse proxy. Background + polling/SSE aman.

## Pilihan actor search

Actor id search **tidak** diturunkan dari `APIFY_ACTOR_PREFIX` — dua actor search tinggal di namespace Apify berbeda:

| Platform | Actor | Kenapa |
| --- | --- | --- |
| instagram | `apify/instagram-scraper` (`searchType:hashtag`) | keyword → post, ada batas bawah tanggal |
| threads | `futurizerush/meta-threads-scraper` (`mode:search`) | `keywords[]` + `start_date`/`end_date` |

Env: `APIFY_SEARCH_ACTOR_INSTAGRAM`, `APIFY_SEARCH_ACTOR_THREADS`.

**Jangan pakai `apify/instagram-search-scraper`.** Tiga masalah yang bikin keyword search tidak pernah menghasilkan post benar:

1. Actor itu me-resolve keyword jadi *halaman place/hashtag*, lalu mengembalikan top-post milik entitas itu — untuk keyword hidup, post-nya 2018–2024.
2. Field input-nya `search` (string koma), bukan `searchTerms[]`. Dikirim `searchTerms[]`, actor mengabaikan keyword dan memakai defaultnya (`"restaurant, restaurant prague"`) — **silent, tanpa error**.
3. Tidak ada batas tanggal atas.

Dua jebakan lain yang sudah dikunci test:

- `maxItems` **query param** tidak dikirim di run-sync. Di actor pay-per-result itu jadi cap charge; kalau terlalu rendah run langsung `ABORTED` ("max charge limit was too low to deliver any posts") sebelum payload dibaca. Batas jumlah hasil sudah ada di payload (`resultsLimit` / `max_posts`).
- Batch menautkan post lewat `IngestRun.PostIDs`, bukan `ListRecentPosts(platform)`. Versi lama bikin batch mengklaim post milik batch/scrape lain.

## Kontrak

### Batch lifecycle

```
PENDING → RUNNING → SUCCEEDED | FAILED
```

- `PENDING`: row dibuat, belum panggil Apify.
- `RUNNING`: `apify_run` dibuat, `RunSearch` in-flight, S3 keys ditulis.
- `SUCCEEDED`: ingest + read-back sukses, counters terisi.
- `FAILED`: Apify status != SUCCEEDED atau ingest error, `error` terisi.

### API

| Method | Path | Auth | Status | Body |
|--------|------|------|--------|------|
| POST | `/api/scrape/keywords` | `act` | `202` | `{ batch: KeywordBatch }` |
| GET | `/api/scrape/batches` | `act` | `200` | `{ batches: KeywordBatch[], total? }` |
| GET | `/api/scrape/batches/:id` | `act` | `200` | `{ batch: KeywordBatch }` |
| GET | `/api/scrape/batches/:id/posts` | `act` | `200` | `{ posts: Post[] }` — posts milik batch (join via batch_posts) |
| GET | `/api/scrape/recent` | `act` | `200` | `{ posts: Post[] }` — tetap, untuk history satuan |
| POST | `/api/scrape/target` | `act` | `200` | `{ post, comments }` — tetap sync |

`KeywordBatch`:
```json
{
  "id": "uuid",
  "platform": "instagram",
  "keywords": ["batulicin"],
  "from": "2026-09-01T00:00:00Z|null",
  "to": "2026-09-24T00:00:00Z|null",
  "maxPosts": 50,
  "actorId": "apify/instagram-scraper",
  "status": "PENDING|RUNNING|SUCCEEDED|FAILED",
  "apifyRunId": "uuid|null",
  "itemsRead": 12,
  "postsCount": 12,
  "commentsCount": 34,
  "error": "string|null",
  "createdBy": "uuid|null",
  "createdAt": "RFC3339",
  "finishedAt": "RFC3339|null"
}
```

### SSE (opsional, reuse hub)

Event `keyword_batch` saat batch terminal — payload `{ id, status }` — FE polling 3s sebagai fallback jika SSE putus.

## Skema

### `keyword_batch` (000014)

```sql
id uuid PK default gen_random_uuid()
platform platform NOT NULL
keywords text[] NOT NULL CHECK (array_length(keywords,1) BETWEEN 1 AND 5)
window_from timestamptz
window_to timestamptz
max_posts int NOT NULL CHECK (1..200)
actor_id text NOT NULL
status text NOT NULL CHECK (PENDING|RUNNING|SUCCEEDED|FAILED)
apify_run_id uuid REFERENCES apify_run(id) ON DELETE SET NULL
items_read int NOT NULL DEFAULT 0
posts_count int NOT NULL DEFAULT 0
comments_count int NOT NULL DEFAULT 0
error text
created_by uuid REFERENCES app_user(id) ON DELETE SET NULL
created_at timestamptz NOT NULL DEFAULT now()
finished_at timestamptz
```

Index: `created_at DESC`, `status`.

### `keyword_batch_post` (join, untuk dashboard batch)

```sql
keyword_batch_id uuid FK keyword_batch(id) CASCADE
post_id uuid FK post(id) CASCADE
PRIMARY KEY (keyword_batch_id, post_id)
```

Diisi setelah ingest: tiap post yang di-upsert selama batch di-link.

### `apify_run.scrape_job_id` nullable (000013)

Keyword batch tidak punya single `scrape_job`; `apify_run` dibuat dengan `scrape_job_id = NULL`. `UNIQUE` tetap — Postgres izinkan banyak NULL.

## Alur

```
FE: POST /api/scrape/keywords {platform, keywords, from,to,maxPosts}
BE: validate → actorFor → INSERT keyword_batch PENDING → go processBatch(batch.id)
    → return 202 {batch}
    goroutine:
      UPDATE batch RUNNING, CreateApifyRun(scrape_job_id=NULL)
      runner.RunSearch → S3 Put → CreateRawPayload
      UPDATE apify_run SUCCEEDED/FAILED
      NewSearchScrapeIngestor(window).IngestRun(runID)
      INSERT keyword_batch_post (batch, postIds)
      UPDATE batch SUCCEEDED {itemsRead,postsCount,commentsCount} | FAILED {error}
      hub.Publish("keyword_batch", {id, status})
FE: polling GET /batches + GET /batches/:id sampai terminal → GET /batches/:id/posts → render dashboard batch
```

## View — bedain satuan vs batch

- **`/scrape`** (existing): form keyword di atas + **Batch list** di bawah (tabel: keywords · platform · date window · status badge · counts · createdAt). Klik row → `/scrape/batches/[id]`.
- **`/scrape/batches/[id]`** (baru): header batch (keywords, window, actor, status timeline, error jika failed) + **Posts dashboard** (grid/tabel posts milik batch: cover, author, caption, likes/comments/views, scrapedAt) + link “Enqueue actions” per post (ke `/actions` dengan prefill).
- **Post satuan**: tetap di `POST /scrape/target` → detail 1 post + comments (dipakai `New Action` form di `/actions`). Tidak masuk batch list.

## Testing

- `service/scrape_keyword_test.go` — validasi dedupe/cap, ingest window, empty page, plus row post flat (bentuk produksi `instagram-scraper`) kena filter window dan window Threads (`created_at` RFC3339). Batch lifecycle diuji lewat `ScrapeKeywords` (poll sampai terminal) dengan `fakeKeywordBatchStore` dari `scrape_fake_test.go`.
- `adapter/apify/runner_test.go` — nama field payload per actor (`search` koma vs `keywords[]`, `onlyPostsNewerThan` vs `start_date`/`end_date`) + window kosong tidak mengirim tanggal nol. Nama field salah itu **silent** (actor pakai default-nya), jadi ini yang menahan regresi.
- `domain/keyword_batch_test.go` — kontrak JSON batch: nama field camelCase. Tanpa ini tag JSON yang hilang lolos compile dan bikin web baca `undefined` di semua field.
- Endpoint HTTP (`POST /scrape/keywords` → 202, `GET /scrape/batches[/:id[/posts]]`) diverifikasi manual end-to-end terhadap Apify sungguhan; belum ada test handler otomatis.
- `make test` (api) + `npx tsc --noEmit` (web) harus hijau sebelum merge.
