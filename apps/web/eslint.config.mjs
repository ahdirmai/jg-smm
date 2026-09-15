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
];
