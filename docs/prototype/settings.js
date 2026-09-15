/* Settings page tab switching + theme controls (prototype only). */
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

    // Appearance: drive the shared, persisted theme and reflect active state.
    const themeBtns = document.querySelectorAll('[data-set-theme]');
    const markActive = () => {
      const current = window.smmTheme ? window.smmTheme.get() : 'dark';
      themeBtns.forEach((b) => {
        const on = b.dataset.setTheme === current;
        b.classList.toggle('btn-primary', on);
        b.classList.toggle('btn-outline', !on);
      });
    };
    themeBtns.forEach((btn) => {
      btn.addEventListener('click', () => {
        window.smmTheme && window.smmTheme.set(btn.dataset.setTheme);
        markActive();
      });
    });
    markActive();
  }

  // shell.js replaces #app on DOMContentLoaded, so wait a tick after it runs.
  window.addEventListener('load', () => setTimeout(init, 0));
})();
