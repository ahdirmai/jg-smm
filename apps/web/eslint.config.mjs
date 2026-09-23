import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { FlatCompat } from '@eslint/eslintrc';
import shared from '../../eslint.config.mjs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

const compat = new FlatCompat({ baseDirectory: __dirname });

export default [
  ...shared,
  ...compat.extends('next/core-web-vitals'),
  {
    rules: {
      // The Next.js plugin owns App Router concerns; keep overrides minimal.
      '@next/next/no-html-link-for-pages': 'off',
    },
  },
  {
    // Playwright fixtures. base.extend takes ({ deps }, use): a fixture needing
    // no dependency writes `async ({}, use)`, and `use` is the fixture setter —
    // neither is React, but rules-of-hooks and no-empty-pattern are written for
    // it, so both fire on every fixture. Scoped to e2e only.
    files: ['e2e/**/*.ts'],
    rules: {
      'no-empty-pattern': 'off',
      'react-hooks/rules-of-hooks': 'off',
    },
  },
];
