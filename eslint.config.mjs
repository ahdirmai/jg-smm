import js from '@eslint/js';
import tseslint from 'typescript-eslint';

/**
 * Shared flat ESLint config for the SMM monorepo (ESLint 9).
 * TypeScript-aware, but deliberately light — the goal is to catch real problems
 * (unused vars, `any` leaks in library code, accidental console in shipped code)
 * without drowning the team in style noise that Prettier already owns.
 */
export default tseslint.config(
  {
    ignores: [
      '**/dist/**',
      '**/.next/**',
      '**/.turbo/**',
      '**/node_modules/**',
      '**/src/generated/**',
      '**/*.config.*',
      // Generated artifacts (gitignored): Playwright trace reports and the
      // prototype sandbox. ESLint flat config does not read .gitignore, so name
      // them here or lint drowns in minified-vendor output.
      '**/e2e/report/**',
      'docs/prototype/**',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
    },
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      '@typescript-eslint/no-explicit-any': 'warn',
      'no-console': ['warn', { allow: ['warn', 'error'] }],
    },
  },
);
