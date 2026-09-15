/* Settings page tab switching + theme toggle (prototype only). */
(function () {
  function init() {
    const tabs = document.querySelectorAll('#settings-tabs [data-tab]');
    const panels = document.querySelectorAll('[data-panel]');
    if (!tabs.length) return;

    const show = (name) => {
      tabs.forEach((t) => {
        if (t.dataset.tab === name) t.setAttribute('aria-current', 'page');
        else t.removeAttribute('aria-current');
      });
      panels.forEach((p) => p.classList.toggle('hidden', p.dataset.panel !== name));
    };

    tabs.forEach((t) => t.addEventListener('click', () => show(t.dataset.tab)));
    show('team');

    document.querySelectorAll('[data-set-theme]').forEach((btn) => {
      btn.addEventListener('click', () => {
        document.documentElement.classList.toggle('dark', btn.dataset.setTheme !== 'light');
      });
    });
  }

  // shell.js replaces #app on DOMContentLoaded, so wait a tick after it runs.
  window.addEventListener('load', () => setTimeout(init, 0));
})();
