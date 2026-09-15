/* =============================================================================
 * Prototype theme + Tailwind Play CDN config.
 *
 * - Seeds the theme from localStorage, else prefers-color-scheme (default dark).
 * - Maps the design tokens (styles.css) into the Play CDN so utilities such as
 *   `bg-card`, `text-muted-foreground`, `border-border` resolve.
 *
 * Load AFTER the Tailwind CDN script and BEFORE <body> renders, everywhere.
 * Static prototype only; the real app configures Tailwind v4 in globals.css.
 * ========================================================================== */
(function () {
  var KEY = 'smm-theme';

  function apply(theme) {
    var root = document.documentElement;
    root.classList.toggle('dark', theme === 'dark');
    root.classList.toggle('light', theme === 'light');
  }

  // Default dark; respect a saved choice, else the OS preference.
  var stored = null;
  try {
    stored = localStorage.getItem(KEY);
  } catch (e) {
    /* ignore (private mode) */
  }
  var prefersLight =
    window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches;
  apply(stored || (prefersLight ? 'light' : 'dark'));

  window.smmTheme = {
    get: function () {
      return document.documentElement.classList.contains('dark') ? 'dark' : 'light';
    },
    set: function (theme) {
      try {
        localStorage.setItem(KEY, theme);
      } catch (e) {
        /* ignore */
      }
      apply(theme);
    },
    toggle: function () {
      this.set(this.get() === 'dark' ? 'light' : 'dark');
      if (window.lucide) window.lucide.createIcons();
    },
  };

  if (window.tailwind) {
    var hsl = function (name) {
      return 'hsl(var(--' + name + ') / <alpha-value>)';
    };
    window.tailwind.config = {
      darkMode: 'class',
      theme: {
        extend: {
          colors: {
            background: hsl('background'),
            foreground: hsl('foreground'),
            card: { DEFAULT: hsl('card'), foreground: hsl('card-foreground') },
            popover: { DEFAULT: hsl('popover'), foreground: hsl('popover-foreground') },
            primary: { DEFAULT: hsl('primary'), foreground: hsl('primary-foreground') },
            secondary: { DEFAULT: hsl('secondary'), foreground: hsl('secondary-foreground') },
            muted: { DEFAULT: hsl('muted'), foreground: hsl('muted-foreground') },
            accent: { DEFAULT: hsl('accent'), foreground: hsl('accent-foreground') },
            destructive: {
              DEFAULT: hsl('destructive'),
              foreground: hsl('destructive-foreground'),
            },
            success: hsl('success'),
            warning: hsl('warning'),
            info: hsl('info'),
            border: hsl('border'),
            input: hsl('input'),
            ring: hsl('ring'),
          },
          borderRadius: {
            lg: 'var(--radius)',
            md: 'calc(var(--radius) - 2px)',
            sm: 'calc(var(--radius) - 4px)',
          },
        },
      },
    };
  }
})();
