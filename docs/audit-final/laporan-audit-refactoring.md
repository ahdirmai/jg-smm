# Audit kode dan rencana refactoring

19 November 2026 · Cabang `redesign/glossy-minimal` · Commit `addf266e3694` · Tahap audit

## 1. Cakupan dan ringkasan eksekutif

- **Pemulihan data perlu perhatian pertama.** Drill dapat menghapus volume lalu gagal menemukan backup. Secara terpisah, parser shell menolak perintah verifikasi arsip sesi di jalur restore. Hindari drill pada data yang perlu dipertahankan.
- **Dua kontrak API mudah berubah saat dirapikan.** Bentuk error berbeda untuk error Go biasa dan error Echo eksplisit. Laporan analytics memeriksa keanggotaan akun melalui daftar yang dipaginasi.
- **Halaman laporan memiliki celah saat berganti jenis.** Jenis yang dipilih dapat dipasangkan dengan hasil permintaan sebelumnya. Cast TypeScript menyembunyikan perbedaan bentuk payload.
- **Risiko refactoring keseluruhan tinggi.** Alasannya mencakup urutan operasi pemulihan, byte yang sudah ditulis saat ekspor gagal, dan transisi UI asinkron. Batch pertama yang layak dieksekusi setelah persetujuan adalah serializer URL laporan/audit. Lima pemanggilnya bisa dibandingkan dengan fixture kecil.

Inventaris Git mencakup 504 berkas terlacak di `apps/` dan 26 di `packages/`, termasuk test, konfigurasi, dan generated code. Enam modul workspace dipetakan. Pembacaan terarah mengikuti alur halaman → klien API → handler → service → repository/SQL, lalu entry point pemulihan dan verifikasi.

| Area | Berkas atau alur yang diperiksa |
|---|---|
| `apps/api` · 371 berkas terlacak | Go 1.26, Echo v4, pgx/v5 dan sqlc. Handler error, audit, health, stream, report, analytics. Service report, report email, analytics, alert, health, team config. Repository analytics, report, team config dan konversi pagination. Query sumber `official_account.sql` dan `report.sql`. Test HTTP/service terkait dan aturan dependensi di `internal/arch/arch_test.go`. |
| `apps/web` · 83 | Next.js 15, React 19. Halaman reports/audit, `lib/api.ts`, hook reports dan empat konsumen SSE. Komponen trend, sentimen, daftar peringkat, engagement breakdown. Konfigurasi Playwright, spec report/audit dan teardown. |
| `apps/worker` · 50 | Konfigurasi, logging, heartbeat, dan cuplikan unit test konfigurasi. |
| `packages/shared`, `packages/ui`, `packages/tsconfig` · 7, 15, 4 | Ekspor dan klien SSE, tipe payload laporan, primitive Table/Badge/Card beserta helper, opsi TS seperti `strict` dan `exactOptionalPropertyTypes`. |
| Lintas modul | Empat skrip `infra/backup/`, bagian skema laporan di `openapi/openapi.yaml`, manifest paket, Turbo, ESLint 9, Prettier, `Makefile`, workflow CI dan pemeriksa tautan dokumen. |

**Batas cakupan.** Angka di atas menghitung seluruh berkas terlacak di setiap modul. Kolom kanan mencantumkan sampel yang diperiksa. Scheduler/provisioner, adapter platform, migrasi historis, deployment Kubernetes/boxd, skrip operasional akun, dan seluruh variasi komponen UI belum mendapat audit fungsi per fungsi. Dependensi, output build, lockfile, arsip, data runtime dan isi backup dilewati. Tipe generated hanya dibuka pada kontrak yang perlu dicocokkan dengan pemanggil. Pemeriksaan aplikasi bersifat statis. Hasil pemeriksaan yang benar-benar dijalankan ada di §5.

## 2. Temuan dan rekomendasi

### Prioritas tinggi

**F01 — Drill dan restore berbeda kontrak tentang argumen backup** · SoC, penamaan/kontrak

Lokasi · `infra/backup/drill.sh` · alur utama, `SRC` · 39–42, 48–68. `infra/backup/restore.sh` · alur utama, `name` dan `SRC` · 12–18. `infra/backup/lib.sh` · `ROOT`, `BACKUP_DIR` · 9–16.

Drill mengambil path absolut lewat `pwd`, menghapus volume, lalu meneruskan path itu ke restore. Restore memperlakukan argumen sebagai nama di bawah direktori backup. Dengan basis sintetis `/audit-fixture/backups`, ekspresi asli menghasilkan `/audit-fixture/backups//audit-fixture/backups/latest`. Pemeriksaan keberadaan direktori dapat gagal setelah penghapusan volume. Karakterisasi pemanggil dan penerima secara offline. Untuk bugfix terpisah, sepakati satu bentuk argumen dan validasi direktori, dump serta manifest yang persis akan dipakai restore sebelum operasi penghapusan pertama.

**F02 — Perintah verifikasi sesi tidak lolos parser shell di dalam container** · SoC, KISS

Lokasi · `infra/backup/restore.sh` · alur utama, blok `got_sessions` · 112–115 dan restart · 118–123. `infra/backup/backup.sh` · pembuatan `sessions.tar` · 82–87.

Skrip luar lolos `bash -n`. String yang dikirim ke `/bin/sh -c` mengandung tanda kutip ganda tanpa penutup di operand `tr -d`. Parser untuk string itu gagal dengan exit 2. Jika arsip sesi ada dan eksekusi mencapai blok ini, verifikasi dapat berhenti sebelum layanan aplikasi dinyalakan kembali. Tambahkan pemeriksaan parser terhadap argumen yang diambil langsung dari sumber. Koreksi tanda kutip dan uji hitungan arsip kosong/berisi merupakan bugfix tersendiri karena mengubah jalur gagal menjadi berhasil.

**F03 — Renderer dapat menerima jenis laporan baru bersama hasil lama** · SoC/SRP, KISS

Lokasi · `apps/web/app/(dashboard)/reports/page.tsx` · `ReportsPage` 53–56, 191–207 dan `ReportBody` 212–215, 278–287, 328–337. `apps/web/lib/hooks/use-reports.ts` · `useReports` 25–53, 62–67. `packages/shared/src/generated/api.ts` · `TargetReport`, `AnalyticsReport` 1404–1411.

Saat pengguna berganti dari targets ke analytics, `kind` baru langsung menentukan cabang render. Hasil targets sebelumnya masih dapat berisi hanya `rows`, sementara cabang analytics mengakses `report.series.length`. Guard `seq` menyaring respons yang datang terlambat. Guard ini tidak memasangkan data yang sudah tampil dengan jenis asalnya. Konsekuensi statisnya adalah potensi TypeError sebelum respons baru tiba. Rekam transisi dengan respons terkendali sebelum memecah komponen. Menyimpan pasangan jenis dan payload secara atomik serta memilih tampilan saat loading adalah usulan bugfix UX yang memerlukan keputusan tersendiri.

**F04 — Arti `error.code` dan `error.message` bergantung pada pembungkus error** · KISS, SoC

Lokasi · `apps/api/internal/http/errors.go` · `errorHandler` 21–39, 42–75. `apps/api/internal/http/audit.go` · `translateAuditError` 87–94. `apps/api/internal/http/errors_test.go` · `TestExplicitConflictKeepsActionableMessage` 16–32, `TestErrorHandlerUnknownStays500` 79–93.

Error Go tak dikenal mendapat kode `internal`. Untuk error Echo dengan pesan string nonkosong, handler menyalin pesan ke `error.code` pada semua status. Hanya 4xx yang juga menyalinnya ke `error.message`. Adapter audit meneruskan `err.Error()` untuk 500 sehingga kode respons dapat memuat teks internal. Test 500 yang disebut di atas memasukkan error Go biasa. Kunci kedua field untuk kedua jalur dengan pesan sintetis, lalu baca pesan Echo satu kali seperti §3. Mengganti kode 500 dengan nilai publik tetap perlu ditangani sebagai perbaikan kontrak tersendiri.

**F05 — Pengecekan akun laporan analytics dibatasi satu halaman database** · KISS, penamaan

Lokasi · `apps/api/internal/service/report.go` · `ReportService.AnalyticsReport` 91–119. `apps/api/internal/repository/analytics.go` · `AnalyticsRepo.ListOfficialAccounts` 102–112. `apps/api/internal/repository/convert.go` · `pageBounds`, `ptrPage` 95–115. `apps/api/db/queries/official_account.sql` · `GetOfficialAccountByID` 5–9, `ListOfficialAccounts` 17–27. `apps/api/internal/http/report.go` · `reportAnalytics`, `mapReportErr` 96–103, 149–156.

Variabel `platform` hanya dipakai untuk menandai bahwa ID ditemukan. Daftar dengan `nil, nil` berarti 100 akun pertama menurut `created_at DESC`, sesudah mengecualikan arsip. Pada endpoint laporan analytics, akun yang ada di luar halaman itu dapat mendapat 404. Lookup langsung per ID juga bisa menemukan akun arsip. Nyatakan maksud lokal dengan boolean sambil mempertahankan query, urutan dan error (§3). Memperluas himpunan akun yang berhak mendapat laporan merupakan keputusan bisnis terpisah.

### Prioritas sedang

**F06 — Ekspor CSV mencampur pengambilan data, pemetaan kolom dan I/O** · SRP, KISS

Lokasi · `apps/api/internal/service/report.go` · `exportCSV` 194–241, `exportJSON` 244–266, `csvHeader` 269–278. `apps/api/internal/http/report.go` · `reportExport` 118–146.

Ada tiga layout baris dan tiga query dalam satu fungsi CSV. Header masuk ke buffer sebelum query, dan `defer cw.Flush()` tetap berjalan saat query gagal. Jadi writer sehat dapat berisi header saja pada saat pemanggil menerima error. JSON mengueri sebelum encode. Ekstrak pemetaan kolom actions dan targets ke fungsi privat murni di paket yang sama. Pertahankan pemilihan query, pengembalian error, timing flush, nama file, urutan kolom dan format waktu di posisinya. Pemeriksaan error tambahan saat flush mengubah kontrak.

**F07 — Empat konverter DTO mengulang aturan nullable yang sama** · DRY

Lokasi · `apps/api/internal/http/analytics.go` · `nullStr`, `nullTime`, `nullInt64` 265–287, `nullFloat64` 346–354, `toContentMetricDTOs`, `toBreakdownDTOs` 322–344, 356–370. `apps/api/internal/http/analytics_test.go` · `TestAnalyticsHandlerBreakdownKeepsAbsentPartsNull` 261–291.

Keempatnya memakai konstruktor nullable yang sama. Penanganan atribut kosong mudah berbeda jika diubah per tipe. Test saat ini memeriksa `saves` melalui pointer hasil decode. Field hilang dan JSON `null` sama-sama dapat menghasilkan pointer nil. Gunakan satu fungsi privat generik dan periksa keberadaan key beserta isi mentahnya. Contoh di §3.

**F08 — Serializer URL audit dan laporan identik** · DRY

Lokasi · `apps/web/lib/api.ts` · `auditQuery`, `reportQuery` 70–88. Pemanggil `reportActions`, `reportTargets`, `reportAnalytics`, `reportExportURL`, `listAudit` 504–523.

Kedua fungsi melewati `undefined` dan string kosong, mempertahankan nilai nol, lalu memakai `URLSearchParams`. Perubahan encoding di salah satu salinan bisa mengubah filter atau URL unduhan. Pertahankan body, gunakan satu fungsi privat dengan parameter `AuditQuery | ReportQuery`. Tipe publik setiap metode, urutan key dan penambahan awalan `?` tetap mengikuti kelima pemanggil saat ini.

**F09 — Tipe dan daftar listener SSE mendefinisikan empat event yang sama** · DRY

Lokasi · `packages/shared/src/stream.ts` · `StreamKind` 20–25, `STREAM_KINDS` 58–63, `openHub` 65–95, `subscribeStream` 103–125.

Menambah anggota union saja sudah lolos pemeriksaan tipe array, meski listener baru belum terdaftar. Jadikan array privat sebagai tuple `as const` dan turunkan tipe publik dengan `(typeof STREAM_KINDS)[number]`. Pertahankan urutan tuple, daftar event yang dipilih UI, alokasi satu koneksi per URL, dan penutupan oleh subscriber terakhir.

### Prioritas rendah

**F10 — Empat hook menduplikasi konfigurasi URL SSE** · Penamaan, magic values/configuration, DRY

Lokasi · konstanta modul `SSE_URL` pada `apps/web/lib/hooks/use-accounts.ts` 9, `apps/web/lib/hooks/use-actions.ts` 9, `apps/web/lib/hooks/use-containers.ts` 11, `apps/web/lib/hooks/use-live-ticker.ts` 7.

Keempat hook membaca variabel publik Next yang sama dengan default literal yang sama. Perubahan parsial dapat membuka koneksi untuk URL yang berbeda. Pindahkan ekspresi persis ke satu modul web. Pertahankan akses langsung `process.env.NEXT_PUBLIC_SSE_URL` untuk substitusi build Next, operator `??`, dan fallback `http://localhost:24080/api/stream`. String kosong saat ini tetap kosong.

**F11 — Dua test konfigurasi worker identik** · DRY, YAGNI

Lokasi · `apps/worker/src/test/unit.test.ts` · `loadConfig applies defaults and trims the API url` 36–47, 132–143. Implementasi `apps/worker/src/core/config.ts` · `loadConfig` 24–43.

Dua blok 12 baris menguji input dan assertion yang sama. Pertahankan kasus trim. Ganti salinan kedua dengan `loadConfig({})` untuk mengunci ID, URL, dan interval default. Dua nama test unik dan dua skenario berbeda tanpa perubahan kode runtime.

**F12 — Referensi kosong `errors.New` sudah kehilangan fungsi** · YAGNI

Lokasi · `apps/api/internal/service/analytics.go` · deklarasi tingkat paket `var _ = errors.New` 522, `AnalyticsService.Create` 73–83.

Paket `errors` memang dipakai oleh `errors.Is`. Deklarasi blank identifier ini memasang referensi ke konstruktor tanpa memanggilnya. Hapus hanya deklarasi tersebut, pertahankan import dan pencocokan error yang ada.

## 3. Tiga contoh refactoring

Cuplikan *sebelum* diambil dari commit audit. Cuplikan *sesudah* adalah usulan. F01–F03 memerlukan pengujian perilaku atau keputusan bugfix. Mengubah outputnya untuk menampilkan contoh refactoring akan melanggar batas pekerjaan ini.

### F04 · Pisahkan pesan Echo eksplisit dari teks status HTTP

**Sebelum** · `apps/api/internal/http/errors.go` · `errorHandler` 60–73

```go
var he *echo.HTTPError
if errors.As(err, &he) {
    httpStatus = he.Code
    if msg, ok := he.Message.(string); ok && msg != "" {
        code = msg
    }
}

message := http.StatusText(httpStatus)
if he != nil && httpStatus >= 400 && httpStatus < 500 {
    if msg, ok := he.Message.(string); ok && msg != "" {
        message = msg
    }
}
```

**Sesudah**

```go
var he *echo.HTTPError
var explicitMessage string
if errors.As(err, &he) {
    httpStatus = he.Code
    explicitMessage, _ = he.Message.(string)
    if explicitMessage != "" {
        code = explicitMessage
    }
}

message := http.StatusText(httpStatus)
if explicitMessage != "" && httpStatus >= 400 && httpStatus < 500 {
    message = explicitMessage
}
```

Pesan diekstrak sekali. Nilai kosong atau non-string mempertahankan kode terdahulu. Error eksplisit 500 dengan pesan sintetis nonkosong masih memakai pesan itu sebagai kode. Penanganan respons committed, pembatalan dan error sentinel mendahului cuplikan ini.

### F05 · Nyatakan bahwa pencarian hanya memeriksa keberadaan ID

**Sebelum** · `apps/api/internal/service/report.go` · `ReportService.AnalyticsReport` 103–113

```go
var platform *domain.Platform
for _, a := range accs {
    if a.ID == *f.AccountID {
        p := a.Platform
        platform = &p
        break
    }
}
if platform == nil {
    return "", nil, fmt.Errorf("%w: unknown official account", domain.ErrNotFound)
}
```

**Sesudah**

```go
accountFound := false
for _, account := range accs {
    if account.ID == *f.AccountID {
        accountFound = true
        break
    }
}
if !accountFound {
    return "", nil, fmt.Errorf("%w: unknown official account", domain.ErrNotFound)
}
```

Nama boolean menyatakan apa yang dipakai oleh cabang error. Kedua versi menerima ID cocok meskipun platform di fixture berupa string kosong. Daftar kosong, batas halaman, pemanggilan trend, dan error terbungkus tetap mengikuti jalur semula.

### F07 · Satu konverter untuk pointer ke JSON nullable

**Sebelum** · `apps/api/internal/http/analytics.go` · `nullInt64` 282–287, satu dari empat salinan

```go
func nullInt64(p *int64) nullable.Nullable[int64] {
    if p == nil {
        return nullable.NewNullNullable[int64]()
    }
    return nullable.NewNullableWithValue(*p)
}
```

**Sesudah**

```go
func nullableFromPtr[T any](p *T) nullable.Nullable[T] {
    if p == nil {
        return nullable.NewNullNullable[T]()
    }
    return nullable.NewNullableWithValue(*p)
}
```

Di pemanggil, `Shares: nullInt64(r.Shares)` menjadi `Shares: nullableFromPtr(r.Shares)`. Terapkan hanya pada keempat konverter yang memakai konstruktor identik. Uji field hadir dengan literal `null`, angka `0`, string `""`, dan waktu nol yang terisi. Field OpenAPI dengan aturan omission berbeda tetap mengikuti mapper masing-masing.

## 4. Task list eksekusi untuk ditinjau

Semua kotak adalah pekerjaan mendatang. Jalankan setiap batch pada baseline yang sama. Selesaikan characterization test sebelum mengubah implementasi terkait.

1. [ ] **T1 — Rekam baseline.** Target root, manifest, CI, dan paket Go/TS. Catat SHA, versi alat, status Git, status setiap perintah §5, skip, dan penyebab kegagalan. **Selesai bila** tersedia matriks sebelum perubahan, termasuk kegagalan lama yang bisa dibedakan dari regresi.
2. [ ] **T2 — Kunci kegagalan pemulihan F01–F02 secara offline.** Target `infra/backup/{drill,restore,lib}.sh` dan fixture uji. Ambil ekspresi path serta argumen shell dari sumber. **Selesai bila** nama biasa, `latest`, path absolut dan status parser terkarakterisasi, tanpa panggilan Docker. Hasil baseline yang menunjukkan cacat dicatat sebagai cacat terbuka. Perubahan protokol atau sintaks produksi perlu persetujuan bugfix.
3. [ ] **T3 — Rekam transisi laporan F03.** Target `reports/page.tsx` di grup dashboard, `lib/hooks/use-reports.ts`, dan test UI. Kendalikan dua promise/response dan pantau error render. **Selesai bila** urutan targets → analytics, loading, hasil terbaru dan respons lama yang terlambat masing-masing punya catatan terukur. Bugfix jenis/payload memiliki keputusan terpisah.
4. [ ] **T4 — Satukan serializer URL F08.** Target `apps/web/lib/api.ts`. Bandingkan lima entry point sebelum dan sesudah memakai satu fungsi privat. **Selesai bila** kelima URL sama persis untuk matriks §5, tipe publik tetap sama, lint dan typecheck web tidak menambah error.
5. [ ] **T5 — Perjelas pencarian F05.** Target `apps/api/internal/service/report.go` dan test laporan. Ganti penanda pointer dengan boolean. **Selesai bila** daftar ID yang diterima, urutan panggilan store, jumlah panggilan trend dan error sama pada kasus ketemu, absen, dan store gagal. Diff SQL serta pagination kosong.
6. [ ] **T6 — Rapikan mapper nullable F07.** Target `apps/api/internal/http/analytics.go` dan test HTTP analytics. Tambahkan assertion key mentah dulu, lalu ganti empat fungsi. **Selesai bila** JSON merepresentasikan null, nol, string kosong dan waktu dengan nilai yang sama dan seluruh pemanggil memakai mapper privat.
7. [ ] **T7 — Pecah pemetaan baris CSV F06.** Target `apps/api/internal/service/report.go` dan `report_test.go`. Pindahkan hanya pembentukan field actions/targets. **Selesai bila** byte CSV, header pada error query, perilaku writer gagal dan urutan call identik. JSON serta handler download tetap lolos uji kontrak.
8. [ ] **T8 — Sederhanakan pembacaan pesan error F04.** Target `apps/api/internal/http/errors.go` dan `errors_test.go`. Setelah characterization test, terapkan §3. **Selesai bila** status, kedua field JSON dan perilaku committed/canceled sama untuk seluruh matriks. Perubahan publik pada respons 500 perlu persetujuan tersendiri.
9. [ ] **T9 — Pusatkan definisi jenis event F09.** Target `packages/shared/src/stream.ts`. Turunkan union dari tuple privat. **Selesai bila** keempat nama listener dan urutannya identik, serta stub EventSource mencatat jumlah buka/tutup yang sama.
10. [ ] **T10 — Pusatkan URL SSE di web F10.** Target empat hook yang disebut pada F10 dan satu modul konfigurasi web. **Selesai bila** build web untuk env absen, kosong dan URL khusus menghasilkan nilai yang sama pada keempat konsumen, tanpa perubahan URL yang dipakai klien.
11. [ ] **T11 — Perbaiki cakupan test konfigurasi F11.** Target `apps/worker/src/test/unit.test.ts`. Ganti salinan kedua dengan kasus env kosong. **Selesai bila** kedua test bernama unik berjalan, menguji input berbeda, jumlah kasus tetap dan kode runtime tidak masuk diff.
12. [ ] **T12 — Hapus deklarasi sisa F12.** Target `apps/api/internal/service/analytics.go`. Hapus satu deklarasi. **Selesai bila** import `errors` masih dipakai, build dan test service tidak menambah kegagalan.
13. [ ] **T13 — Rekonsiliasi hasil sebelum/sesudah.** Target semua batch yang disetujui. Ulangi matriks §5 dan tinjau diff lengkap termasuk file baru. **Selesai bila** setiap perubahan punya bukti ekuivalensi, hasil skip/gagal dijelaskan, dan bugfix yang belum disetujui tidak masuk perubahan runtime.

## 5. Strategi dan rencana test

### Hasil aktual tahap audit

| Pemeriksaan | Hasil aktual |
|---|---|
| Git awal | `git --no-optional-locks status --short --branch` menunjukkan cabang pada header, tanpa perubahan awal. |
| Sintaks empat skrip pemulihan | `bash -n` dijalankan per berkas pada `lib.sh`, `backup.sh`, `restore.sh`, `drill.sh`. Empat kali exit 0. |
| Sintaks perintah tertanam F02 | Argumen `-c` di baris 114 diekstrak dari file dan diberikan melalui stdin ke `/bin/sh -n`. **Gagal, exit 2**, tanda kutip ganda tidak tertutup. Ini pemeriksaan parser, tanpa akses ke arsip. |
| Pembentukan path F01 | Dua assignment asli baris 12–13 diuji di shell dengan basis sintetis. Argumen `latest` menghasilkan satu prefix. Argumen absolut menghasilkan prefix ganda. Tidak ada perintah backup/restore yang dijalankan. |
| Tautan dokumen sebelum dan sesudah laporan | `python3 -B scripts/check_docs_links.py` memeriksa 974 referensi, **gagal, exit 1**. Satu referensi lama di `docs/platforms/youtube.md:60` menuliskan path dari root, sedangkan checker meresolusinya relatif terhadap dokumen asal. |
| Integritas laporan | Tiga cuplikan “sebelum” cocok dengan rentang baris sumber setelah normalisasi indentasi. Rentang bukti diperiksa. Status akhir Git hanya menampilkan laporan baru. |
| Unit/integrasi Go, test worker, typecheck, lint, build, Playwright | **Belum dijalankan.** Status kelulusan aplikasi dan reproduksi runtime F03 belum diketahui. |

### Perintah baseline setelah persetujuan eksekusi

Toolchain repo menetapkan Node 22, pnpm 9.12.0 dan Go 1.26. Jalankan perintah JS dari root. Jalankan perintah Go dari `apps/api/`. Catat exit code per perintah. Rujukan implementasi ada di `package.json`, manifest workspace, `Makefile` 79–99 dan `.github/workflows/ci.yml` 48–58, 114–127.

| Pemeriksaan | Perintah atau prasyarat |
|---|---|
| JS/TS monorepo | `pnpm lint`, `pnpm typecheck`, `pnpm build`, `pnpm test`. Turbo menjalankan script yang tersedia pada masing-masing paket. Worker memakai TypeScript dan `node --test`. Manifest web, shared dan UI tidak mendefinisikan script `test`. |
| API | Dari root `make fmt-check`. Dari `apps/api/` jalankan `go vet ./...`, `go build ./...`, lalu `go test -count=1 ./...` di lingkungan uji. `go test ./internal/arch` memeriksa dependensi antarlapisan. |
| Integrasi PostgreSQL | Untuk menjalankan test repository, sediakan DB uji sekali pakai dengan skema yang sesuai. Set `SMM_TEST_DB=1` dan `DATABASE_URL` eksplisit ke database tersebut, lalu `go test -v -count=1 ./internal/repository`. Nama DB harus mengandung `test`. Suite dapat melakukan TRUNCATE. Tanpa opt-in, kasus dapat berstatus SKIP meski package hijau. Bukti di `apps/api/internal/repository/repo_test.go` · `testDSN` 20–52. |
| UI | Untuk alur tampilan F03 gunakan fixture respons sintetis yang dapat ditahan/dilepas dan tangkap `pageerror`. Spec lama report/audit bisa dijalankan di stack uji terisolasi dengan `pnpm --filter @smm/web exec playwright test --config=e2e/playwright.config.ts reports.spec.ts audit.spec.ts`. Set URL API dan web khusus uji. Spec membuat data dan konfigurasi mendaftarkan global teardown. Arahkan semua layanan ke fixture lokal, termasuk pekerjaan latar. Lihat `apps/web/e2e/playwright.config.ts` 12–27, 51–58 dan `apps/web/e2e/fixtures/teardown.ts` 20–42. |

### Verifikasi per batch dan edge case

| Batch | Pemeriksaan terarah dan oracle |
|---|---|
| T2 · pemulihan | Stub atau evaluasi ekspresi tanpa menjalankan entry point operasional. Uji nama backup, `latest`, path absolut, arsip sesi ada/tidak ada. Parser luar dan parser untuk argumen dalam diuji terpisah. Baseline gagal pada F02 tetap tercatat. |
| T3 · pergantian laporan | Mulai dari targets berhasil dengan `{rows: []}`. Pilih analytics, tahan respons baru, amati DOM/error selama transisi, lalu lepaskan respons. Uji juga dua permintaan sejenis selesai terbalik, respons error terbaru, dan error permintaan lama. Bandingkan jenis data, loading, error, serta permintaan yang boleh menulis state. |
| T4 · URL | Capture URL pada batas fetch atau pembuat URL ekspor. Uji objek kosong, `undefined`, string kosong, `offset: 0`, spasi dan `&`, serta urutan properti. Bandingkan byte URL dan keberadaan `?`. Jalankan `pnpm --filter @smm/web lint` dan `pnpm --filter @smm/web typecheck` setelah dependensi workspace dibangun. |
| T5 · analytics report | `go test -count=1 ./internal/service -run 'TestAnalyticsReport'`. Tambahkan fake yang mencatat argumen dan urutan panggilan. Uji ID nil, tak ditemukan, cocok dengan platform kosong, metric kosong → followers, error daftar dan error trend. Pada fixture SQL dengan urutan waktu unik, bandingkan akun urutan ke-100/101 dan arsip. Pastikan kasus di luar halaman tidak tiba-tiba mendapat series. |
| T6 · JSON nullable | `go test -count=1 ./internal/http -run 'TestAnalytics'`. Decode ke `map[string]json.RawMessage` dan periksa keberadaan key, untuk membedakan key absen dari key bernilai null. Uji nihil vs 0, teks kosong, waktu nol dan nilai terisi. Pastikan koleksi kosong tetap menghasilkan bentuk JSON sebelumnya. |
| T7 · ekspor | `go test -count=1 ./internal/service -run 'Test(Export|ActionReport|AnalyticsReport|ReportEmail)'`. Golden bytes untuk tiga jenis, nol/satu/banyak baris, koma/kutip/newline, waktu dengan offset dan angka nol. Injeksi error query serta writer gagal pada write atau flush. Bandingkan byte yang sempat ditulis, error, nama/jenis lampiran dan urutan query. `report_test.go` 106–165 baru menguji sebagian kolom dan format. |
| T8 · error HTTP | `go test -count=1 ./internal/http -run 'Test(ErrorHandler|ExplicitConflict)'`. Uji error biasa vs Echo eksplisit, sentinel terbungkus, status 400/409/500, pesan kosong/non-string, cancel/EOF, dan response committed. Assert status, code, message serta tidak adanya penulisan kedua. Gunakan teks sintetis. |
| T9–T10 · SSE | Fake EventSource dengan dua subscriber pada URL sama, subscriber URL lain, event valid/JSON rusak dan event error. Bandingkan nama/urutan listener, kredensial, penerima event, jumlah koneksi, dan saat koneksi terakhir ditutup. Ulangi dengan env absen/kosong/khusus. Jalankan `pnpm --filter @smm/shared build`, lint/typecheck shared dan web, lalu build web. |
| T11–T12 · pembersihan | `pnpm --filter @smm/shared build` lalu `pnpm --filter @smm/worker test`. `loadConfig({})` harus menghasilkan `worker-local`, `http://api:8080`, interval 30 detik. Untuk T12 jalankan `go test -count=1 ./internal/service` dan `go build ./...`. |

**Pemeriksaan diff.** Sebelum dan sesudah setiap batch jalankan `git status --short --untracked-files=all`, `git diff --stat`, `git diff --check`, dan tinjau patch terhadap baseline, termasuk perubahan staged dan file baru. Bandingkan kontrak di OpenAPI, SQL, tipe generated, lockfile, public exports, response HTTP/unduhan dan efek samping terkait. Diff kosong pada file kontrak saja perlu dilengkapi dengan perbandingan hasil fixture. Catat regresi, kegagalan yang sudah ada dan test yang dilewati secara terpisah.

## 6. Klarifikasi dan keputusan

Rencana refactoring murni tidak memerlukan klarifikasi tambahan. Eksekusinya menunggu persetujuan. F01–F03, sanitasi kode 500 pada F04 dan perluasan lookup pada F05 memerlukan persetujuan bugfix terpisah bila perilaku itu ingin diubah. Sampai ada keputusan, task terkait membatasi diri pada pembuktian perilaku lama dan perubahan lokal yang ekuivalen.
