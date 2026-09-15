/* =============================================================================
 * Prototype shell — injects the shared top bar + side nav into #app and marks
 * the active route. Keeps every page DRY without a build step.
 * Static prototype only; the real app uses apps/web/components/dashboard-shell.tsx.
 * ========================================================================== */
(function () {
  const NAV = [
    { href: 'dashboard.html', label: 'Dashboard', icon: 'layout-dashboard' },
    { href: 'workers.html', label: 'Workers', icon: 'monitor-smartphone' },
    { href: 'accounts.html', label: 'Accounts', icon: 'users' },
    { href: 'actions.html', label: 'Actions', icon: 'activity' },
    { href: 'templates.html', label: 'Templates', icon: 'message-square-text' },
    { href: 'monitoring.html', label: 'Monitoring', icon: 'gauge' },
    { href: 'audit.html', label: 'Audit Log', icon: 'scroll-text' },
    { href: 'settings.html', label: 'Settings', icon: 'settings' },
  ];

  const current = (location.pathname.split('/').pop() || 'dashboard.html').toLowerCase();

  const icon = (name, cls = 'w-4 h-4') =>
    `<i data-lucide="${name}" class="${cls}"></i>`;

  const navHtml = NAV.map((item) => {
    const active = item.href.toLowerCase() === current;
    return `<a class="nav-item" href="${item.href}"${active ? ' aria-current="page"' : ''}>
      ${icon(item.icon)}<span>${item.label}</span></a>`;
  }).join('');

  const title = document.body.dataset.title || 'Dashboard';
  const subtitle = document.body.dataset.subtitle || '';
  const actions = document.body.dataset.actions || '';

  const html = `
  <div class="flex min-h-screen">
    <aside class="hidden md:flex w-56 shrink-0 flex-col border-r border-border bg-card">
      <a href="dashboard.html" class="flex h-14 items-center gap-2 border-b border-border px-4">
        <span class="grid h-7 w-7 place-items-center rounded-md bg-primary text-primary-foreground">
          ${icon('activity', 'w-4 h-4')}
        </span>
        <span class="text-sm font-semibold tracking-tight">SMM Automation</span>
      </a>
      <nav class="flex-1 space-y-1 p-2">${navHtml}</nav>
      <div class="border-t border-border p-3">
        <div class="flex items-center gap-2.5">
          <span class="grid h-8 w-8 place-items-center rounded-full bg-secondary text-xs font-semibold">AD</span>
          <div class="min-w-0">
            <div class="truncate text-sm font-medium">ahdirmai</div>
            <div class="truncate text-xs muted">owner@local.test</div>
          </div>
        </div>
      </div>
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <header class="flex h-14 shrink-0 items-center gap-3 border-b border-border px-4 md:px-6">
        <div class="min-w-0">
          <h1 class="truncate text-base font-semibold tracking-tight">${title}</h1>
          ${subtitle ? `<p class="truncate text-xs muted">${subtitle}</p>` : ''}
        </div>
        <div class="ml-auto flex items-center gap-2">
          <div class="hidden lg:flex items-center gap-2 rounded-md border border-border bg-background px-2.5 h-9 text-sm muted w-56">
            ${icon('search', 'w-4 h-4')}<span>Search accounts, jobs…</span>
            <kbd class="ml-auto rounded border border-border px-1.5 text-xs">⌘K</kbd>
          </div>
          ${actions}
        </div>
      </header>

      <main class="flex-1 p-4 md:p-6">
        <div id="page"></div>
      </main>
    </div>
  </div>

  <div id="proto-banner" class="fixed bottom-3 left-3 z-50 rounded-md border border-border bg-popover px-3 py-1.5 text-xs muted shadow-lg">
    Prototype · <span class="text-foreground font-medium">static mock</span> — not wired to API
  </div>`;

  document.addEventListener('DOMContentLoaded', () => {
    const app = document.getElementById('app');
    if (!app) return;
    // Everything the page author wrote inside #app becomes the page body.
    const pageBody = app.innerHTML;
    app.outerHTML = html;
    document.getElementById('page').innerHTML = pageBody;
    if (window.lucide) window.lucide.createIcons();
  });
})();
