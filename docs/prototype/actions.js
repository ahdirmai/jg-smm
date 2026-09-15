/* =============================================================================
 * Dummy action process (prototype).
 *
 * Simulates the real end-to-end action pipeline so the UX can be reviewed before
 * the worker exists: a job moves through
 *   Queued → Dispatched → Running → Verifying → Success | Failed
 * with realistic delays. "Simulate failure" exercises the failed branch
 * (cooldown gate / verification failure), as documented in DEVELOPMENT_RULE §7.4.
 *
 * Focus is Action-to-Target: comment, like, report (post & comment), reply.
 * Nothing here touches a backend.
 * ========================================================================== */
(function () {
  const STEPS = [
    { key: 'queued', label: 'Queued', ms: 600 },
    { key: 'dispatched', label: 'Dispatched', ms: 900 },
    { key: 'running', label: 'Running', ms: 1500 },
    { key: 'verifying', label: 'Verifying', ms: 1000 },
  ];

  const BADGE = {
    queued: 'badge',
    dispatched: 'badge badge-info',
    running: 'badge badge-info',
    verifying: 'badge badge-warning',
    success: 'badge badge-success',
    failed: 'badge badge-destructive',
  };

  const DOT = {
    queued: 'dot dot-busy dot-pulse',
    dispatched: 'dot dot-busy dot-pulse',
    running: 'dot dot-busy dot-pulse',
    verifying: 'dot dot-busy dot-pulse',
    success: 'dot dot-ready',
    failed: 'dot dot-error',
  };

  let seq = 100;

  function stepperMarkup(id) {
    const dots = STEPS.map(
      (s) => `<span class="stepper-dot" data-step="${s.key}" title="${s.label}"></span>`,
    ).join('');
    return `<div class="stepper" data-stepper="${id}">${dots}</div>`;
  }

  function setRowState(row, state, note) {
    const badge = row.querySelector('[data-role="status"]');
    const dot = row.querySelector('[data-role="dot"]');
    const noteEl = row.querySelector('[data-role="note"]');
    if (badge) badge.className = BADGE[state] || 'badge';
    if (badge) badge.textContent = state.charAt(0).toUpperCase() + state.slice(1);
    if (dot) dot.className = DOT[state] || 'dot';
    if (noteEl && note !== undefined) noteEl.textContent = note;

    // Light the step dots up to the current state.
    const order = STEPS.map((s) => s.key);
    const upto = order.indexOf(state);
    row.querySelectorAll('.stepper-dot').forEach((d, i) => {
      d.classList.toggle('done', upto === -1 ? true : i <= upto);
      d.classList.toggle('active', i === upto);
    });
  }

  function run(row, { fail } = {}) {
    let i = 0;
    const tick = () => {
      if (i < STEPS.length) {
        const step = STEPS[i];
        setRowState(row, step.key, `${step.label}…`);
        i += 1;
        setTimeout(tick, step.ms);
        return;
      }
      // Verdict: success, or the failed branch (cooldown / verification).
      const failed = fail !== undefined ? fail : Math.random() < 0.2;
      if (failed) {
        setRowState(row, 'failed', 'FAILED — cooldown gate blocked (60s)');
      } else {
        const ts = new Date().toISOString().slice(11, 19);
        setRowState(row, 'success', `Verified on feed · ${ts}`);
        const dur = row.querySelector('[data-role="duration"]');
        if (dur) dur.textContent = (2 + Math.random() * 2).toFixed(1) + 's';
      }
      row.dataset.busy = 'false';
      row.querySelectorAll('[data-role="action"]').forEach((b) => (b.disabled = false));
    };
    setRowState(row, 'queued', 'Queued…');
    row.dataset.busy = 'true';
    row.querySelectorAll('[data-role="action"]').forEach((b) => (b.disabled = true));
    setTimeout(tick, 300);
  }

  function newRow(action, target, text) {
    seq += 1;
    const id = `job_${seq.toString(16)}`;
    const tr = document.createElement('tr');
    tr.dataset.busy = 'false';
    tr.innerHTML = `
      <td class="mono">${id}</td>
      <td class="max-w-[16rem] truncate">${target}</td>
      <td><span class="badge">${action}</span></td>
      <td><div class="flex items-center gap-2">
        <span data-role="dot" class="dot dot-busy dot-pulse"></span>
        <span data-role="status" class="badge">Queued</span>
        ${stepperMarkup(id)}
      </div></td>
      <td class="text-right mono" data-role="duration">—</td>
      <td class="text-right mono">0/3</td>
      <td class="text-right whitespace-nowrap" data-role="note">Queued…</td>
      <td class="text-right">
        <button class="btn btn-ghost btn-icon btn-sm" data-role="action" title="Cancel" disabled>
          <i data-lucide="x" class="w-4 h-4"></i>
        </button>
      </td>`;
    return tr;
  }

  function init() {
    const tbody = document.getElementById('dummy-rows');
    if (!tbody) return;

    // "Trigger action" buttons: {action, target, text}
    document.querySelectorAll('[data-trigger]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const action = btn.dataset.trigger;
        const target = btn.dataset.target || 'instagram.com/p/Cx1…';
        const row = newRow(action, target);
        tbody.prepend(row);
        if (window.lucide) window.lucide.createIcons();
        run(row);
      });
    });

    // "Simulate failure" demonstrates the failed branch deterministically.
    const failBtn = document.querySelector('[data-trigger-fail]');
    if (failBtn) {
      failBtn.addEventListener('click', () => {
        const action = failBtn.dataset.triggerFail;
        const row = newRow(action, failBtn.dataset.target || 'instagram.com/p/Cw9…');
        tbody.prepend(row);
        if (window.lucide) window.lucide.createIcons();
        run(row, { fail: true });
      });
    }
  }

  window.addEventListener('load', () => setTimeout(init, 0));
})();
