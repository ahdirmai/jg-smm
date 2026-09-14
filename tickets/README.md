# Ticket Index — MVP-1-SMM

Satu file per tiket (generated dari `TICKETS.md`). Jangan edit manual — jalankan `python3 scripts/gen_tickets.py`.

## P0 — Foundation & Walking Skeleton

| ID | Judul | Pri | Est | File |
|----|-------|-----|-----|------|
| P0-01 | Monorepo init | P0 | S | [p0_01.md](p0_01.md) |
| P0-02 | Compose stack (lokal) | P0 | M | [p0_02.md](p0_02.md) |
| P0-03 | BE skeleton Go | P0 | M | [p0_03.md](p0_03.md) |
| P0-04 | DB migration base | P0 | M | [p0_04.md](p0_04.md) |
| P0-05 | sqlc setup | P0 | S | [p0_05.md](p0_05.md) |
| P0-06 | Auth + RBAC 3 role | P0 | M | [p0_06.md](p0_06.md) |
| P0-07 | OpenAPI SSOT | P0 | M | [p0_07.md](p0_07.md) |
| P0-08 | FE skeleton Next.js | P0 | M | [p0_08.md](p0_08.md) |
| P0-09 | Worker skeleton | P0 | M | [p0_09.md](p0_09.md) |
| P0-10 | CI pipeline | P0 | M | [p0_10.md](p0_10.md) |
| P0-11 | Pre-commit & Makefile | P0 | S | [p0_11.md](p0_11.md) |
| P0-12 | ADR bootstrap | P1 | S | [p0_12.md](p0_12.md) |

## P1 — Account Lifecycle & Provisioning

| ID | Judul | Pri | Est | File |
|----|-------|-----|-----|------|
| P1-01 | ERD → migrations | P0 | M | [p1_01.md](p1_01.md) |
| P1-02 | Domain & port | P0 | M | [p1_02.md](p1_02.md) |
| P1-03 | K8s provisioner | P0 | L | [p1_03.md](p1_03.md) |
| P1-04 | Desired-state reconciler | P0 | L | [p1_04.md](p1_04.md) |
| P1-05 | Assign akun (bin-packing) | P0 | M | [p1_05.md](p1_05.md) |
| P1-19 | Create Container (manual) | P0 | M | [p1_19.md](p1_19.md) |
| P1-06 | ProvisionLog + orphan sweeper | P0 | M | [p1_06.md](p1_06.md) |
| P1-07 | Credential crypto | P0 | S | [p1_07.md](p1_07.md) |
| P1-08 | Redis transport BE | P0 | M | [p1_08.md](p1_08.md) |
| P1-09 | Worker BLPOP loop | P0 | M | [p1_09.md](p1_09.md) |
| P1-10 | Worker login headful | P0 | L | [p1_10.md](p1_10.md) |
| P1-11 | Control channel worker | P0 | M | [p1_11.md](p1_11.md) |
| P1-12 | 2FA/checkpoint flow | P0 | L | [p1_12.md](p1_12.md) |
| P1-13 | Callback endpoints BE | P0 | M | [p1_13.md](p1_13.md) |
| P1-14 | Proxy binding | P1 | M | [p1_14.md](p1_14.md) |
| P1-15 | Add Account UI | P0 | M | [p1_15.md](p1_15.md) |
| P1-16 | Account ops (pause/resume/remove) | P0 | M | [p1_16.md](p1_16.md) |
| P1-17 | SSE stream BE | P0 | M | [p1_17.md](p1_17.md) |
| P1-18 | Account list page FE | P1 | M | [p1_18.md](p1_18.md) |

## P2 — Scrape (IG + Threads) & Monitoring

| ID | Judul | Pri | Est | File |
|----|-------|-----|-----|------|
| P2-01 | ERD scrape | P0 | M | [p2_01.md](p2_01.md) |
| P2-02 | Scrape scheduler | P0 | M | [p2_02.md](p2_02.md) |
| P2-03 | Apify adapter | P0 | L | [p2_03.md](p2_03.md) |
| P2-04 | Ingest pipeline | P0 | M | [p2_04.md](p2_04.md) |
| P2-05 | Metric aggregation | P0 | M | [p2_05.md](p2_05.md) |
| P2-06 | Session refresh job | P1 | M | [p2_06.md](p2_06.md) |
| P2-07 | Alert engine | P1 | M | [p2_07.md](p2_07.md) |
| P2-08 | Monitoring dashboard FE | P1 | M | [p2_08.md](p2_08.md) |
| P2-09 | Live monitoring (deferred) | P2 | S | [p2_09.md](p2_09.md) |

## P3 — Action Engine (Playwright)

| ID | Judul | Pri | Est | File |
|----|-------|-----|-----|------|
| P3-01 | ERD action | P0 | M | [p3_01.md](p3_01.md) |
| P3-02 | Template engine | P0 | M | [p3_02.md](p3_02.md) |
| P3-03 | Ban-word detector | P1 | S | [p3_03.md](p3_03.md) |
| P3-04 | Worker platform adapter (IG) | P0 | L | [p3_04.md](p3_04.md) |
| P3-05 | Worker platform adapter (Threads) | P0 | L | [p3_05.md](p3_05.md) |
| P3-06 | Platform registry (OCP) | P0 | S | [p3_06.md](p3_06.md) |
| P3-07 | Sequential controller | P0 | M | [p3_07.md](p3_07.md) |
| P3-08 | Ground-truth verify | P0 | M | [p3_08.md](p3_08.md) |
| P3-09 | Cooldown gate | P0 | S | [p3_09.md](p3_09.md) |
| P3-10 | Rate limit scheduler | P0 | M | [p3_10.md](p3_10.md) |
| P3-11 | Action callback + retry | P0 | M | [p3_11.md](p3_11.md) |
| P3-12 | Error classification | P0 | M | [p3_12.md](p3_12.md) |
| P3-13 | Action UI | P0 | M | [p3_13.md](p3_13.md) |
| P3-14 | CDP screenshot | P0 | S | [p3_14.md](p3_14.md) |

## P4 — Dashboard Productization & Operator UX

| ID | Judul | Pri | Est | File |
|----|-------|-----|-----|------|
| P4-01 | ContainerCard (container + accounts) | P0 | M | [p4_01.md](p4_01.md) |
| P4-02 | Job Queue virtualized | P0 | M | [p4_02.md](p4_02.md) |
| P4-03 | SSE realtime lengkap | P0 | M | [p4_03.md](p4_03.md) |
| P4-04 | Report builder | P1 | M | [p4_04.md](p4_04.md) |
| P4-05 | Export CSV/JSON | P1 | S | [p4_05.md](p4_05.md) |
| P4-06 | Scheduled report email | P2 | M | [p4_06.md](p4_06.md) |
| P4-07 | Bulk import CSV | P0 | M | [p4_07.md](p4_07.md) |
| P4-08 | Live ticker + LiveBrowserModal | P1 | M | [p4_08.md](p4_08.md) |
| P4-09 | RBAC UI per role | P1 | S | [p4_09.md](p4_09.md) |
| P4-10 | Storybook + a11y | P1 | M | [p4_10.md](p4_10.md) |

## P5 — Hardening, Scale & GA

| ID | Judul | Pri | Est | File |
|----|-------|-----|-----|------|
| P5-01 | Scale test ~50 container | P0 | L | [p5_01.md](p5_01.md) |
| P5-02 | Health score + quarantine | P0 | M | [p5_02.md](p5_02.md) |
| P5-03 | Observability lengkap | P0 | M | [p5_03.md](p5_03.md) |
| P5-04 | Security review | P0 | M | [p5_04.md](p5_04.md) |
| P5-05 | Backup & restore | P0 | M | [p5_05.md](p5_05.md) |
| P5-06 | Runbook + severity matrix | P0 | M | [p5_06.md](p5_06.md) |
| P5-07 | Load test FE/BE | P1 | M | [p5_07.md](p5_07.md) |
| P5-08 | Cost monitoring | P1 | S | [p5_08.md](p5_08.md) |
| P5-09 | Docs final sync | P0 | M | [p5_09.md](p5_09.md) |
| P5-10 | GA sign-off | P0 | L | [p5_10.md](p5_10.md) |

**Total: 74 tiket.**
