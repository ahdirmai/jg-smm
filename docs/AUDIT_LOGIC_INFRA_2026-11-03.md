# Audit logic dan infra — 3 November 2026

Scope: kode workspace termasuk perubahan pengguna yang belum di-commit. Audit statis terfokus, bukan sertifikasi seluruh aplikasi.

## Update terakhir
HEAD 057fb75, 2 November 2026 11:26 WITA: cap worker fleet. Sebelumnya f335bb8, 19 Oktober: sentiment, insight ranking, multi-platform batches, threaded replies. Workspace memiliki 64 tracked files berubah sebelum audit, serta file untracked. Snapshot September tidak mewakili workspace sekarang.

## Temuan dan tindakan
- P1: Waker memanggil StartWorker langsung tanpa pemeriksaan kapasitas reconciler. Ditangani dengan CapacityDriver bersama untuk CreateWorker dan StartWorker, admission diserialkan, gagal menghitung menolak start, worker yang sudah berjalan dapat dipanggil ulang. Uji 30 permintaan serentak menerima tepat 5.
- P1: default ELASTIC_POOL_MAX_ACTIVE=0 menonaktifkan batas. Default kode, Compose, dan contoh env diubah ke 5. Env eksplisit 0 masih mematikan batas; konfigurasi deployment harus diperiksa sebelum start.
- P1: API Kubernetes 3 replica memiliki admission per proses. Manifest diubah ke satu replica dan Recreate agar rollout tidak menumpuk API. Konsekuensi: downtime API saat rollout. Horizontal scaling memerlukan distributed lock/admission; jangan scale API sebelum itu.
- P2: parser Redis menghilangkan expectedMention dan templateId. Field kini dipertahankan agar validasi author reply dan callback template bekerja.
- Terbuka P1: ElasticPool.Decide tidak melindungi aksi/login yang sedang berjalan. Queue kosong setelah BLPOP dapat dianggap idle, dan over-cap dapat menghentikan worker dengan pekerjaan aktif. Heartbeat selalu browserStatus idle. Perlu status busy/auth dan admission drain sebelum stop.
- Terbuka P2: workerLastSeen memakai heartbeat sebagai aktivitas jika belum pernah ada aksi, sehingga heartbeat reguler dapat menghalangi idle eviction worker yang belum pernah bekerja.
- Terbuka P2: heartbeat dimulai sebelum control subscription selesai; READY belum membuktikan subscriber tersedia. Waker dapat publish login terlalu dini.
- Terbuka: uji worker autofill credential gagal (fills kosong), perlu diagnosis fixture versus runtime sebelum menyimpulkan login rusak.

## Verifikasi
Go internal/config, internal/service, cmd/server lulus. TestCapacityDriver lulus. Worker TypeScript build lulus; suite worker gagal pada autofill login. Docker ps: tidak ada stack/container SMM aktif. Tidak melakukan login, scraping berbayar, atau deployment. Batas runtime belum diverifikasi. Guard membatasi admission baru, tidak otomatis menghentikan fleet yang sudah melewati batas. Satu proses API mengendalikan fleet; proses di luar driver ini tidak tercakup.

## Runtime checkpoint setelah startup berhasil

3 November 2026: image Timescale selesai diunduh per potongan dan diverifikasi
SHA256, kemudian dimuat ke Docker. PostgreSQL/Redis/MinIO dan API healthy;
/readyz melaporkan seluruh dependensi up. Migrasi 23 clean, 485 akun Instagram
terbaca. Satu worker aktif berhasil claim dan heartbeat. Dashboard /login HTTP
200. Batas konfigurasi 5 aktif; belum ada uji beban lima worker atau login
Instagram. API memakai binary kode workspace, image worker memakai image lokal
yang sudah tersedia sebelumnya dan belum dibangun ulang. Catatan verifikasi
sebelumnya di atas adalah keadaan sebelum startup ini.
