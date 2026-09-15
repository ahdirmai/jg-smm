/* =============================================================================
 * Per-platform analytics renderer (prototype).
 *
 * Each analytics-<platform>.html defines window.ANALYTICS with its own metric
 * list (platform-specific — IG has saves/reels views, YouTube has watch time,
 * etc.) and this script renders the KPI strip + trend chart from that config,
 * so pages stay declarative and DRY while the metrics stay platform-correct.
 *
 * Data is mocked. In the real app these figures come from the 3rd-party
 * analytics provider (ingested by the BE), never from worker accounts.
 * ========================================================================== */
(function () {
  const PALETTE = [
    'hsl(var(--status-busy))',
    'hsl(var(--status-ready))',
    'hsl(var(--status-error))',
    'hsl(var(--warning))',
    'hsl(var(--info))',
  ];

  function fmt(n) {
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
    if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
    return String(n);
  }

  function kpiCard(m, i) {
    const up = m.delta >= 0;
    const color = i === 0 ? 'text-primary' : up ? 'success' : 'destructive';
    return `
      <div class="card p-4">
        <div class="flex items-center justify-between">
          <span class="text-xs uppercase tracking-wide muted">${m.label}</span>
          <i data-lucide="${m.icon || 'activity'}" class="w-4 h-4 muted"></i>
        </div>
        <div class="kpi-value mt-2">${fmt(m.value)}</div>
        <div class="mt-1 text-xs">
          <span class="${up ? 'text-success' : 'text-destructive'}">${up ? '▲' : '▼'} ${Math.abs(m.delta).toFixed(1)}%</span>
          <span class="muted">vs prev</span>
        </div>
      </div>`;
  }

  // Two normalised series -> responsive SVG lines, no chart library.
  function trendChart(cfg) {
    const w = 600;
    const h = 200;
    const series = [
      { name: cfg.metrics[0].label, data: cfg.seriesA, color: PALETTE[0] },
      { name: cfg.metrics[1].label, data: cfg.seriesB, color: PALETTE[1] },
    ];
    const all = series.flatMap((s) => s.data);
    const min = Math.min(...all);
    const max = Math.max(...all);
    const span = max - min || 1;
    const pt = (v, idx, len) => {
      const x = (idx / (len - 1)) * w;
      const y = h - ((v - min) / span) * (h - 20) - 10;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    };
    const lines = series
      .map(
        (s) =>
          `<polyline fill="none" stroke="${s.color}" stroke-width="2.5" points="${s.data
            .map((v, i) => pt(v, i, s.data.length))
            .join(' ')}" />`,
      )
      .join('');
    const legend = series
      .map(
        (s) =>
          `<span class="flex items-center gap-1.5"><span class="dot" style="background:${s.color}"></span>${s.name}</span>`,
      )
      .join('');
    const grid = [40, 80, 120, 160]
      .map(
        (y) =>
          `<line x1="0" y1="${y}" x2="${w}" y2="${y}" stroke="hsl(var(--border))" stroke-width="1" />`,
      )
      .join('');
    return `
      <div class="card p-4 lg:col-span-2">
        <div class="mb-3 flex items-center justify-between">
          <div><h2 class="text-sm font-semibold">${cfg.trendTitle || 'Trend'}</h2>
            <p class="text-xs muted">${cfg.metrics[0].label} · ${cfg.metrics[1].label} · 30 days</p></div>
          <div class="flex items-center gap-3 text-xs">${legend}</div>
        </div>
        <div class="h-56 w-full rounded-md border border-border bg-background/40 p-2">
          <svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none" class="h-full w-full">
            <g>${grid}</g>${lines}
          </svg>
        </div>
      </div>`;
  }

  function postRows(cfg) {
    return (cfg.posts || [])
      .map(
        (p) => `<tr>
        <td class="max-w-[22rem] truncate">${p.title}</td>
        <td><span class="badge ${p.kind === 'Reel' || p.kind === 'Video' || p.kind === 'Short' ? 'badge-info' : ''}">${p.kind}</span></td>
        <td class="text-xs muted">${p.date}</td>
        <td class="text-right mono">${fmt(p.m1 ?? 0)}</td>
        <td class="text-right mono">${fmt(p.m2 ?? 0)}</td>
        <td class="text-right mono">${fmt(p.m3 ?? 0)}</td>
      </tr>`,
      )
      .join('');
  }

  function render() {
    const cfg = window.ANALYTICS;
    if (!cfg) return;
    const kpis = document.getElementById('kpis');
    const chart = document.getElementById('chart');
    const posts = document.getElementById('posts');
    const head = document.getElementById('post-metrics');
    const empty = document.getElementById('analytics-empty');

    if (cfg.comingSoon) {
      if (empty) empty.classList.remove('hidden');
    }

    if (kpis) kpis.innerHTML = cfg.metrics.slice(0, 4).map(kpiCard).join('');
    if (chart) chart.innerHTML = trendChart(cfg);
    if (head)
      head.innerHTML = `<th class="text-right">${cfg.metrics[0].label}</th><th class="text-right">${cfg.metrics[1].label}</th><th class="text-right">${(cfg.metrics[2] || cfg.metrics[0]).label}</th>`;
    if (posts) posts.innerHTML = postRows(cfg);

    if (window.lucide) window.lucide.createIcons();
  }

  window.addEventListener('load', () => setTimeout(render, 0));
})();
