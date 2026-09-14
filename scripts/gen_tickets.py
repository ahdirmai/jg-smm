#!/usr/bin/env python3
"""
Generate one markdown file per ticket into tickets/ from the SSOT (TICKETS.md).

Usage:  python3 scripts/gen_tickets.py
SSOT:   TICKETS.md  (markdown tables per phase)
Output: tickets/<phase>_<n>.md + tickets/README.md (index)
"""

import os
import re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SSOT = os.path.join(ROOT, "TICKETS.md")
OUT = os.path.join(ROOT, "tickets")

# Phase metadata: title, goal/context, technical notes, test-plan hints.
PHASES = {
    "P0": {
        "name": "Foundation & Walking Skeleton",
        "context": "Fase fondasi: repo, tooling, container, DB, auth, dan kerangka end-to-end. Semua fase berikut bergantung pada ini. Walking skeleton mengunci kontrak (OpenAPI, migration, container) sejak awal.",
        "notes": "Utamakan YAGNI — hanya bangun yang dipakai P1. Semua service: non-root, read-only rootfs, drop capabilities, HEALTHCHECK, resource request+limit. **Lingkungan build = lokal Mac M2 16 GB (docker-compose, ARM64, tanpa K8s)** — lihat `INFRA_ANALYST.md` §15.1/§15.2.",
        "tests": "Unit untuk logika murni; integration via testcontainers/envtest; CI wajib hijau (lint+test+build+Trivy).",
    },
    "P1": {
        "name": "Account Lifecycle & Provisioning",
        "context": "Fase inti operasional: operator menambah akun dari UI → BE provisioning container → worker login headful interaktif → session tersimpan. Tanpa akun aktif, tidak ada scrape/action.",
        "notes": "Invarian kunci: 1 container = 1 device = N akun, maks 1 per platform (@@unique([workerId, platform])). Channel: queue:action:<workerId> + control-<workerId>. Commit DB SEBELUM publish/enqueue.",
        "tests": "Integration akun dummy end-to-end; envtest/kind untuk K8s; unit untuk reconciler diff & packing matrix.",
    },
    "P2": {
        "name": "Scrape (IG + Threads) & Monitoring",
        "context": "Fase data: scrape via Apify actor (K8s Job ephemeral), ingest raw → MinIO → Postgres, metric time-series, alert.",
        "notes": "Rate limit dipaksa scheduler BE, bukan di dalam job. Raw payload di MinIO, normalisasi idempotent (upsert by externalId). TimescaleDB untuk MetricSnapshot.",
        "tests": "Integration mock actor; unit normalizer per platform; re-run ingest tidak duplikat.",
    },
    "P3": {
        "name": "Action Engine (Playwright)",
        "context": "Fase auto-engagement (paling berisiko ban): worker eksekusi comment & like sequential lintas akun dengan template + verifikasi ground truth.",
        "notes": "Invarian: worker hanya SUBSCRIBE Redis + POST callback, tidak pernah sentuh DB. Verdict = baris upsert UNIQUE (actionJobId, attempt). Tidak ada SUCCESS tanpa verifikasi. Cooldown gate 60 dtk per (akun, target).",
        "tests": "Unit adapter dengan DOM fixture; integration akun dummy; contract test callback ke handler BE (replay fixture).",
    },
    "P4": {
        "name": "Dashboard Productization & Operator UX",
        "context": "Fase produk: operator mengelola ratusan akun + job dari UI tanpa menyentuh server; strategist/analyst dapat report.",
        "notes": "Realtime via SSE (frame entitas penuh, invalidation by id). Virtualisasi tabel > 1000 baris. Server Component default; TanStack Query untuk server state. Bundle budget per route.",
        "tests": "E2E untuk flow kritis; a11y axe-core; perf (virtualized + lighthouse).",
    },
    "P5": {
        "name": "Hardening, Scale & GA",
        "context": "Fase produksi: stabil di skala target (~50 container / 100 akun), aman, terobservasi, runbook lengkap → GA.",
        "notes": "Health score + auto-quarantine. Observability: Prometheus/Grafana/Loki + alert Slack. DR drill. Security sign-off wajib sebelum GA.",
        "tests": "Load/soak test; chaos (matikan node); DR drill restore; acceptance PRD v1.0.",
    },
}

# Optional per-ticket addendum, prepended above the phase notes.
TICKET_NOTES = {
    "P0-01": "Monorepo harus build di **arm64** (Apple Silicon). Sertakan `.nvmrc`/`go.work`/`.tool-versions` dan catat prasyarat toolchain (Node 22, Go 1.23, pnpm, OrbStack) di `README.md` root.",
    "P0-02": "Target utama **lokal Mac M2 16 GB**: orchestration = **docker-compose (bukan K8s)**. Set `PROVISIONER_MODE=static` (BE tak panggil K8s API), `ACTION_DRY_RUN=true` default, `ACTION_BATCH_PARALLELISM=2`. Pin image **multi-arch/arm64** (Playwright `*-noble`, timescale, minio, redis) dan sediakan `--scale worker=3` (maks 3 di M2). **Runtime lokal = OrbStack** (set VM RAM ~8 GiB; bukan Docker Desktop). Lihat `SYSTEM_DESIGN.md` §Local Tier & `INFRA_ANALYST.md` §15.2.",
    "P1-03": "Provisioner **dual-mode**: `PROVISIONER_MODE=k8s` (prod) vs `static` (lokal — daftar worker dari baris `Worker` hasil seed, tak memanggil K8s API). Logika tetap 1 port `K8sClient`/`Provisioner` interface; adapter `static` = no-op. envtest hanya jalan bila Docker/K8s tersedia.",
    "P1-04": "Reconciler = **jalur produksi**. Di lokal tak aktif (tidak ada K8s API); diuji via unit test murni (`diff(desired,actual)`) + envtest opsional. Jangan jadikan reconciler dependensi keras untuk `make up` lokal.",
}

DOO = """- [ ] PR ≤ 400 LOC, review ≥ 1 approver.
- [ ] Lint + typecheck + test hijau (CI).
- [ ] Docs terdampak di-update (OpenAPI/Storybook/runbook).
- [ ] AC semua terpenuhi. Tiket ditutup."""


def split_cells(line):
    # strip leading/trailing pipe, split on unescaped pipes
    line = line.strip()
    if line.startswith("|"):
        line = line[1:]
    if line.endswith("|"):
        line = line[:-1]
    return [c.strip() for c in line.split("|")]


def parse():
    rows = []
    phase = None
    with open(SSOT, encoding="utf-8") as f:
        for raw in f:
            line = raw.rstrip("\n")
            m = re.match(r"^##\s+(P\d)\s+—\s+(.+)$", line)
            if m:
                phase = m.group(1)
                continue
            if line.startswith("|") and not re.match(r"^\|\s*-+", line):
                cells = split_cells(line)
                if cells and cells[0] == "ID":
                    continue
                if cells and re.match(r"^P\d-\d+$", cells[0]):
                    rows.append(
                        {
                            "id": cells[0],
                            "title": cells[1],
                            "scope": cells[2],
                            "ac": cells[3],
                            "est": cells[4],
                            "dep": cells[5],
                            "pri": cells[6],
                            "phase": phase,
                        }
                    )
    return rows


def ac_lines(ac):
    ac = ac.strip()
    if ac.startswith("- [ ]"):
        return ac
    # split on ';' heuristically -> bullet per clause
    parts = [p.strip() for p in re.split(r";\s*", ac) if p.strip()]
    if len(parts) <= 1:
        return "- [ ] " + ac
    return "\n".join("- [ ] " + p for p in parts)


def write_ticket(r):
    ph = PHASES[r["phase"]]
    body = f"""# {r["id"]} — {r["title"]}

> Phase: **{r["phase"]} — {ph["name"]}** · Priority: **{r["pri"]}** · Estimate: **{r["est"]}** · Depends on: {r["dep"]}

## Context

{ph["context"]}

Tiket ini: {r["title"]}.

## Scope

{r["scope"]}

## Technical Notes

{TICKET_NOTES.get(r["id"], "") + (chr(10) + chr(10) if r["id"] in TICKET_NOTES else "")}{ph["notes"]}

## Acceptance Criteria

{ac_lines(r["ac"])}

## Test Plan

{ph["tests"]}

## Definition of Done

{DOO}

## References

- `DEVELOPMENT_RULE.md`, `DEVELOPMENT_PHASE.md`, `TICKETS.md`.
- Phase doc: `DEVELOPMENT_PHASE.md` → {r["phase"]} ({ph["name"]}).
"""
    fn = os.path.join(OUT, r["id"].replace("-", "_").lower() + ".md")
    open(fn, "w", encoding="utf-8").write(body)


def write_index(rows):
    by_phase = {}
    for r in rows:
        by_phase.setdefault(r["phase"], []).append(r)
    lines = [
        "# Ticket Index — MVP-1-SMM",
        "",
        "Satu file per tiket (generated dari `TICKETS.md`). Jangan edit manual — jalankan `python3 scripts/gen_tickets.py`.",
        "",
    ]
    for pid in sorted(by_phase):
        ph = PHASES[pid]
        lines.append(f"## {pid} — {ph['name']}")
        lines.append("")
        lines.append("| ID | Judul | Pri | Est | File |")
        lines.append("|----|-------|-----|-----|------|")
        for r in by_phase[pid]:
            fn = r["id"].replace("-", "_").lower() + ".md"
            lines.append(
                f"| {r['id']} | {r['title']} | {r['pri']} | {r['est']} | [{fn}]({fn}) |"
            )
        lines.append("")
    lines.append(f"**Total: {len(rows)} tiket.**")
    lines.append("")
    open(os.path.join(OUT, "README.md"), "w", encoding="utf-8").write("\n".join(lines))


def main():
    os.makedirs(OUT, exist_ok=True)
    rows = parse()
    for r in rows:
        write_ticket(r)
    write_index(rows)
    print(f"generated {len(rows)} tickets into tickets/")


if __name__ == "__main__":
    main()
