# Design System

> Base: **shadcn/ui** (Radix + Tailwind + CSS variables). Theme: **dark default, indigo accent**. Gaya: **minimalis profesional** — restrained palette, generous whitespace, hierarki via size/weight bukan warna.

## 1. Stack

- **shadcn/ui** — komponen inti. Install via CLI, simpan source di `packages/ui` agar bisa di-share ke `apps/web`.
- **Tailwind CSS v4** — utility-first. Theme tokens didefinisikan di `globals.css` sebagai CSS variables.
- **Radix UI** — primitif accessibility (otomatis dari shadcn).
- **lucide-react** — ikon. Sudah jadi default shadcn.
- **next-themes** — toggle dark/light.
- **cmdk** — command bar.
- **sonner** — toast.
- **recharts** — chart (MetricSnapshot).
- **@tanstack/react-virtual** — virtualized table (Job Queue).
- **class-variance-authority + clsx + tailwind-merge** — variant helper (sudah dari shadcn).

## 2. Instalasi

> **Implementasi (P0-08).** shadcn components disimpan sebagai source di `packages/ui/src/components` (diekspor via `@smm/ui`), bukan via alias `@/components/ui` per-app. `tailwindcss` + `@tailwindcss/postcss` dipakai versi **4.1.x** (v4.0.0 bentrok dengan scanner internal Next 15 saat build). Token CSS ada di `apps/web/app/globals.css`; `@source` di sana memindai `packages/ui/src` agar utility yang dipakai komponen shared ikut ter-emit. Font: `Inter` (`--font-inter`) + `JetBrains Mono` via `next/font`. Dark di-set dengan class `dark` di `<html>` + `next-themes` (`defaultTheme=dark`).

CLI shadcn tetap dipakai untuk menambah komponen baru (outputnya diarahkan ke `packages/ui/src/components`):

```bash
pnpm dlx shadcn@latest init -d
pnpm dlx shadcn@latest add button card input label textarea select \
  dialog sheet popover dropdown-menu tooltip command tabs badge \
  separator skeleton scroll-area switch checkbox table sonner
```

Tambah manual (tidak ada di registry default):

- `ContainerCard` — wrapper Card khusus 1-akun.
- `StatusDot` — Badge varian status browser/job.
- `RoleGuard` — render berdasarkan role user.
- `BanWordDetector` — Textarea + regex highlight.
- `MetricChart` — Card + recharts line/area.
- `JobQueue` — Table + virtualisasi + SSE patch.
- `Sparkline` — mini line chart inline (SVG, tanpa recharts) di dalam kartu.
- `StatCard` — KPI card (label + nilai + delta + ikon).
- `Icon` — wrapper brand icon (IG/Threads) sebagai custom SVG component (`Icon.Instagram`, `Icon.Threads`).
- `LiveBrowserModal` — iframe noVNC (live screen browser worker) untuk selesaikan challenge; hanya muncul saat `authStatus=NEEDS_INPUT`.
- `ChallengeDialog` — input kode OTP/checkpoint + screenshot challenge → `POST /accounts/{id}/input`.

## 3. Prinsip Minimalis Profesional

1. **Hierarki via size & weight, bukan warna.** Hindari >2 warna status di satu view.
2. **Whitespace = luxury.** Padding default card `p-6`; section gap `gap-6`.
3. **Border tipis 1px**, warna `--border`. Hindari shadow berlebihan.
4. **Restrained accent.** `--primary` hanya untuk CTA primer + brand. Tidak untuk data viz (pakai chart palette).
5. **Status = ikon + label + dot warna.** Tidak warna saja.
6. **Typography tegas.** Inter Variable UI, JetBrains Mono untuk ID/angka/log.
7. **Density toggle**: `compact` (default operator, h-8 row) vs `comfortable` (strategist, h-12 row). Disimpan di Zustand + cookie.
8. **No gradient, no glassmorphism.** Flat dengan elevasi 1px border.

## 4. Tokens

shadcn convention: HSL disimpan di CSS variables, dipanggil via Tailwind.

### 4.1 Color (HSL tanpa wrapper)

```css
/* globals.css */
@layer base {
  :root {
    --background: 0 0% 100%;
    --foreground: 222 47% 11%;
    --card: 0 0% 100%;
    --card-foreground: 222 47% 11%;
    --popover: 0 0% 100%;
    --popover-foreground: 222 47% 11%;
    --primary: 238 84% 67%; /* indigo-500, brand */
    --primary-foreground: 0 0% 100%;
    --secondary: 220 14% 96%;
    --secondary-foreground: 222 47% 11%;
    --muted: 220 14% 96%;
    --muted-foreground: 220 9% 46%;
    --accent: 220 14% 96%;
    --accent-foreground: 222 47% 11%;
    --destructive: 0 84% 60%;
    --destructive-foreground: 0 0% 100%;
    --success: 142 71% 45%;
    --warning: 38 92% 50%;
    --info: 199 89% 60%;
    --border: 220 13% 91%;
    --input: 220 13% 91%;
    --ring: 238 84% 67%;
    --status-cold: 220 9% 46%;
    --status-ready: 142 71% 45%;
    --status-busy: 238 84% 67%;
    --status-error: 0 84% 60%;
    --chart-1: 238 84% 67%;
    --chart-2: 0 0% 9%;
    --chart-3: 0 84% 60%;
    --chart-4: 38 92% 50%;
    --chart-5: 199 89% 60%;
    --chart-6: 142 71% 45%;
    --radius: 0.5rem;
  }

  .dark {
    --background: 222 47% 5%; /* lebih pekat dari default shadcn */
    --foreground: 220 13% 95%;
    --card: 222 47% 7%;
    --card-foreground: 220 13% 95%;
    --popover: 222 47% 7%;
    --popover-foreground: 220 13% 95%;
    --primary: 238 84% 67%;
    --primary-foreground: 222 47% 5%;
    --secondary: 222 30% 12%;
    --secondary-foreground: 220 13% 95%;
    --muted: 222 30% 10%;
    --muted-foreground: 220 9% 60%;
    --accent: 222 30% 12%;
    --accent-foreground: 220 13% 95%;
    --destructive: 0 72% 51%;
    --destructive-foreground: 0 0% 100%;
    --success: 142 71% 45%;
    --warning: 38 92% 50%;
    --info: 199 89% 60%;
    --border: 222 30% 14%;
    --input: 222 30% 14%;
    --ring: 238 84% 67%;
    --status-cold: 220 9% 46%;
    --status-ready: 142 71% 45%;
    --status-busy: 238 84% 67%;
    --status-error: 0 72% 51%;
    --chart-1: 238 84% 67%;
    --chart-2: 0 0% 85%;
    --chart-3: 0 72% 51%;
    --chart-4: 38 92% 50%;
    --chart-5: 199 89% 60%;
    --chart-6: 142 71% 45%;
  }
}
```

### 4.2 Typography

| Use     | Tailwind class                          | Style                     |
| ------- | --------------------------------------- | ------------------------- |
| Display | `text-3xl font-semibold tracking-tight` | Halaman title             |
| H1      | `text-2xl font-semibold`                | Section heading           |
| H2      | `text-xl font-semibold`                 | Sub-section               |
| Body    | `text-sm`                               | Konten utama              |
| Small   | `text-xs text-muted-foreground`         | Helper, label tabel       |
| Mono    | `font-mono text-xs`                     | ID job, container ID, log |

Font: `Inter Variable` (UI), `JetBrains Mono` (mono). Load via `next/font`.

### 4.3 Spacing & Radius

- Spacing: Tailwind default (4-base). Container padding `p-4` / `p-6`. Section `space-y-6`.
- Radius: `--radius: 0.5rem` (8 px). Card `rounded-lg`. Pill `rounded-full`.

### 4.4 Breakpoints

| Name  | Min width | Layout                              |
| ----- | --------- | ----------------------------------- |
| `sm`  | 640       | Side nav collapse jadi icon-only    |
| `md`  | 768       | Container grid 2 kol                |
| `lg`  | 1024      | Default desktop, grid 3 kol         |
| `xl`  | 1280      | Operator console wide, grid 4 kol   |
| `2xl` | 1536      | Multi-panel dengan secondary column |

## 5. Komponen

### 5.1 shadcn UI (install via CLI)

| Komponen           | Customization                                                                      |
| ------------------ | ---------------------------------------------------------------------------------- |
| `Button`           | Tambah variant `destructive` (sudah ada di shadcn) untuk kill container.           |
| `Card`             | Default pattern: header + content + footer.                                        |
| `Table`            | Wrap dengan `@tanstack/react-virtual` untuk `JobQueue`.                            |
| `Dialog`/`Sheet`   | Drawer default pakai `Sheet` (kanan). Modal pakai `Dialog`.                        |
| `Input`/`Textarea` | Label via `Label`, helper via `text-xs text-muted-foreground`.                     |
| `Select`           | Single + multi via Radix. Async via TanStack Query di handler.                     |
| `Tabs`             | Untuk switch platform IG/Threads.                                                  |
| `Badge`            | Variant `success` `warning` `destructive` `info` `outline` saja.                   |
| `Toast` (sonner)   | 3 level: `default` (top-right, 4 dtk), `warning`, `critical` (top-center, sticky). |
| `Tooltip`          | Delay 300 ms.                                                                      |
| `Command` (cmdk)   | ⌘K command bar.                                                                    |
| `Skeleton`         | Untuk container grid loading.                                                      |
| `ScrollArea`       | Untuk drawer body panjang.                                                         |
| `Separator`        | Horizontal default `bg-border`.                                                    |
| `DropdownMenu`     | Action container card.                                                             |
| `Switch`           | Toggle pause/resume container.                                                     |
| `Checkbox`         | Bulk select tabel.                                                                 |
| `Popover`          | Filter facet tanggal.                                                              |

### 5.2 Custom (build on shadcn)

#### `ContainerCard`

Basis: shadcn `Card`. **1 kartu = 1 container (device)**. Header = container (region, status, heartbeat, queue depth, Pause/Resume). Body = daftar akun di dalamnya (maks 1 per platform), tiap akun satu baris: ikon platform + `@handle` + `AuthStatusBadge` + status.

```tsx
<Card className="p-4">
  <CardHeader className="flex flex-row items-center gap-3 p-0 pb-3">
    <span className="font-mono text-xs text-muted-foreground">{workerName}</span>
    <Badge variant="outline">US-CA</Badge>
    {desiredState === 'STOPPED' && <Badge variant="outline">Paused</Badge>}
    <div className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
      <StatusDot variant={browserStatus} />
      <span>hb: {heartbeatAge}s</span>
    </div>
  </CardHeader>
  <CardContent className="p-0 space-y-2">
    {accounts.map((a) => (
      <div key={a.id} className="flex items-center gap-2 text-sm">
        <PlatformIcon platform={a.platform} className="h-4 w-4" />
        <span className="font-medium">@{a.handle}</span>
        <AuthStatusBadge status={a.authStatus} />
        <DropdownMenu>{/* per-akun: remove, re-login, lihat session */}</DropdownMenu>
      </div>
    ))}
    <Sparkline data={throughput} />
    {provisionErr && <div className="text-xs text-destructive">provision: {provisionErr}</div>}
    <div className="flex items-center justify-between">
      <Badge variant="outline">queue: {queueDepth}</Badge>
      <DropdownMenu>
        <DropdownMenuItem onClick={pause}>Pause</DropdownMenuItem>
        <DropdownMenuItem onClick={resume}>Resume</DropdownMenuItem>
        <DropdownMenuItem onClick={restartBrowser}>Restart browser</DropdownMenuItem>
        <DropdownMenuItem onClick={remove} className="text-destructive">
          Remove (2-step confirm)
        </DropdownMenuItem>
      </DropdownMenu>
    </div>
  </CardContent>
</Card>
```

Empty states:

- **Cold-start**: `<Skeleton className="h-32 w-full" />` + label "Booting browser..." + ETA.
- **Paused**: kartu redup (`opacity-60`) + label "Paused — session ditahan" + tombol Resume.
- **Provisioning error**: border `destructive` + `provisionErr` + tombol Retry / Remove.
- **No jobs**: ilustrasi minimal `lucide-react/Inbox` + "No pending jobs" + tombol `Create action`.

Aksi kartu = mutasi desired-state (Pause/Resume/Remove) — semua lewat API idempotent (Idempotency-Key); UI optimistis → reconcile dulu koreksi via SSE `account-updated`.

#### `StatusDot`

Badge 8 px lingkaran, hanya warna + aria-label. Dipakai di mana warna status relevan.

```tsx
const map = {
  cold: { color: 'bg-[hsl(var(--status-cold))]', label: 'Cold' },
  ready: { color: 'bg-[hsl(var(--status-ready))]', label: 'Ready' },
  busy: { color: 'bg-[hsl(var(--status-busy))]', label: 'Busy' },
  error: { color: 'bg-[hsl(var(--status-error))]', label: 'Error' },
} as const;
```

#### `LiveBrowserModal` & `ChallengeDialog`

- `LiveBrowserModal`: `Dialog` large + iframe streaming noVNC internal service pod (`/novnc/{accountId}/vnc.html` — di-proxy BE, auth RBAC operator/owner hanya, `X-Frame-Options`同源). Header: "Selesaikan challenge di browser live — jangan isi kredensial lain." Loading state + fallback text jika stream putus.
- `ChallengeDialog`: screenshot challenge (dari `ActionLog`/account) + Input OTP (`inputMode=numeric`) + Submit → `POST /accounts/{id}/input`. Status `needs_input` berulang → dialog tetap terbuka, update screenshot.
- `AuthStatusBadge` (varian Badge): `AUTHENTICATING`=info, `NEEDS_INPUT`=warning + ikon kunci, `AUTHENTICATED`=success, `FAILED`=destructive + tooltip `error_message`.

#### `RoleGuard`

Render children hanya jika role user termasuk.

```tsx
<RoleGuard allow={['OPERATOR', 'OWNER']}>
  <Button variant="destructive">Kill</Button>
</RoleGuard>
```

#### `BanWordDetector`

Textarea shadcn + highlight regex spans overlay. Validasi inline + counter.

#### `MetricChart`

shadcn `Card` + recharts `<ResponsiveContainer>` line/area. Warna stroke dari `--chart-1..6`.

#### `JobQueue`

shadcn `Table` + `@tanstack/react-virtual`. Group header per akun, sequential numbering. SSE event patch 1 baris saja (TanStack Query invalidation by id).

#### `Sparkline`

Mini line chart inline (SVG murni, tanpa recharts) — busana throughput/queue di `ContainerCard`. Props `{ data: number[] }`. Warna stroke `--chart-1`.

#### `StatCard`

KPI card: label + nilai besar + delta (▲/▼ dengan warna success/destructive) + ikon opsional. Basis shadcn `Card`. Dipakai di KPI strip dashboard Strategist.

#### `Icon`

Wrapper brand icon sebagai custom SVG component (`Icon.Instagram`, `Icon.Threads`, dst.) — lucide tidak punya brand mark. Ukuran default `h-5 w-5`, `currentColor`.

## 6. Layout

- **Shell**: top bar 56 px + side nav 56 (icon) / 224 (expanded) + content.
- **Density toggle** persisted in cookie.
- **Container grid**: `grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4`.
- **Dashboard sections**: 3 kolom `lg:grid-cols-3` (Strategist view), 1 kolom penuh (Operator view).

## 7. Pola UI Kritis

### Dashboard Strategist

- KPI strip atas: 4 `Stat Card` (views, likes, comments, reach).
- Trend chart: `<MetricChart>` 30 hari.
- Top post table: 10 baris, sortable.
- Report CTA: tombol `Export CSV` kanan-atas.

### Operator Console

- **Top bar**: tombol "Add account" (primer), "Bulk import" (sekunder), filter facet, search.
- **Filter bar**: shadcn `Select` (platform, status, region) + date `Popover`.
- **Container grid**: pakai `ContainerCard`. Jumlah kartu = jumlah container (device), tiap kartu memuat akun-akunnya; status provisioning/pause/live dari SSE. Grid auto-fit, virtualisasi bila > 50 kartu.
- **Live ticker kanan**: SSE event stream (`action-updated`, `account-updated`, `worker-health`, `provision-updated`) — virtualized list.

### Add Account Flow (Modal multi-step — login interaktif)

- Step 1: pilih platform (IG/Threads) + `username` + `password` (`Input type=password`). Helper: "kredensial di-encrypt AES-GCM; hanya worker pemilik akun yang bisa login."
- Step 2: pilih proxy group (`Select` async dari BE) + region auto-suggest.
- Step 3: confirm → submit. BE encrypt → **bin-pack** akun ke container ber-slot kosong (atau auto-create container baru) → create pod+PVC → `PUBLISH control-<workerId> auth-login` (payload `accountId`). Operator **tidak** memilih container.
- Stepper real-time via SSE: `Creating pod → Booting browser → Waiting login → Ready`. Branch: `Needs input` (2FA/checkpoint) → tombol **Buka live screen** (`LiveBrowserModal`) + dialog `auth-input` kode OTP (bisa berulang). Cancel disabled selama K8s create in-flight.
- On success: akun baru muncul sebagai baris di ContainerCard (baru atau existing) + Badge `authenticated` (green) + toast "@handle connected".
- On failure: tampilkan error class (`K8S_QUOTA`, `CREDENTIALS_REJECTED`, `BOOT_TIMEOUT`) + tombol retry (re-enroll login, tanpa recreate pod) / remove.

### Bulk Import

- Upload CSV (`platform,handle,cookie,proxy_group`). Max 100 baris per upload.
- BE parse + validasi per baris (CSV kolom `platform,username,password,proxy_group`). Response `{queued, invalid, rate_limited}`.
- Bulk create rate-limit `10/menit` ke K8s API; progress via SSE event `bulk-progress {done, total}`.
- Setelah bulk: roster akun tampil dengan badge `AUTHENTICATING` → beres ke `AUTHENTICATED`/`NEEDS_INPUT`/`FAILED` via SSE — operator kerjakan challenge satu-satu (antri di drawer).

### Job Queue

- Group per akun (collapsible).
- Baris: ID mono, target, type badge, scheduled, status, duration, retry.
- Filter: shadcn `DropdownMenu` facet.
- Drawer (Sheet kanan): payload, attempt history (`ActionLog`), screenshot.

### Comment Composer

- shadcn `Textarea` + `BanWordDetector`.
- Variable chips: shadcn `Button` variant `outline` size `sm`.
- Preview card: replace var dengan nilai aktual.

## 8. State & Data

- **Server state**: TanStack Query. `queryKey` hierarchical.
- **UI state**: Zustand. Store: `density`, `theme`, `selectedAccountIds`.
- **SSE**: satu koneksi `EventSource` global (hook `useEventStream`). Frame = objek entitas **penuh** (bukan delta) → `queryClient.setQueryData` per id. Jangan mirror state. Reconnect: `retry: 2000` dari server; saat reconnect lakukan refetch penuh daftar aktif (tanpa replay event).
- **Forms**: react-hook-form + `@hookform/resolvers/zod`.

## 9. Aksesibilitas

- shadcn sudah Radix-based — accessible by default. **Jangan override focus ring.**
- Kontras teks ≥ 4.5:1 di dark & light.
- ARIA live region untuk toast & SSE event.
- Reduced motion: hormati `prefers-reduced-motion`.
- Keyboard: semua interaksi reachable; shortcut layer via `?`.

## 10. Do / Don't

**Do**

- Pakai shadcn primitives langsung. Wrap hanya jika memang custom.
- Komponen di `packages/ui/` punya `<ComponentName>` test visual di Storybook.
- Import order: `cn` helper dulu, lalu shadcn, lalu local.
- Variant dari `cva` di file yang sama dengan komponen.

**Don't**

- ❌ Modifikasi file hasil `shadcn add` langsung. Buat wrapper di `apps/web/components/`.
- ❌ Pakai warna status tanpa label + ikon.
- ❌ Pakai shadow untuk elevasi (pakai border + background beda).
- ❌ Auto-refresh seluruh tabel (pakai SSE patch per id).
- ❌ Konfirmasi irreversible via `Dialog` sederhana — wajib 2 langkah (ketik "KILL" atau hold 3 dtk).
- ❌ Paralel tab di container yang sama.

## 11. Performance

- Bundle budget per route ≤ 200 KB gzip first-load.
- Lazy import: recharts, modal berat, drawer konten.
- Image: `next/image` dengan `priority` hanya di atas-fold.
- Virtualization wajib untuk tabel > 50 baris dan container grid > 100 kartu.

## 12. Konvensi File

```
packages/ui/
├── src/
│   ├── components/
│   │   ├── ui/                # shadcn output, jangan edit
│   │   ├── container-card.tsx
│   │   ├── status-dot.tsx
│   │   ├── role-guard.tsx
│   │   ├── ban-word-detector.tsx
│   │   ├── metric-chart.tsx
│   │   ├── job-queue.tsx
│   │   ├── sparkline.tsx
│   │   ├── stat-card.tsx
│   │   ├── live-browser-modal.tsx
│   │   ├── challenge-dialog.tsx
│   │   └── icon.tsx
│   ├── lib/
│   │   └── utils.ts           # cn() helper
│   └── styles/
│       └── globals.css        # tokens
└── package.json
```

Component di-import di `apps/web` via path alias `@/components/ui` atau `@ui/*`.
