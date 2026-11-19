/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',
  /**
   * Dev and production MUST NOT share an output directory.
   *
   * `next build` writes manifests + a compiled server into `.next/`; `next dev`
   * writes its own cache there and reads whatever manifests it finds. When both
   * have run, the dev server loads a PRODUCTION manifest against its dev
   * webpack runtime and every page dies with
   * `TypeError: e[o] is not a function` — the error reports the runtime, never
   * the collision, so it looks like an app bug.
   *
   * `next build` (and the Dockerfile, which copies `.next/standalone`) keeps
   * `.next`; the dev server gets `.next-dev`. A stale build can no longer
   * poison a running dev session.
   */
  distDir: process.env.NEXT_DIST_DIR || '.next',
  reactStrictMode: true,
  // Consume workspace packages that ship TypeScript source (ui) or built dist (shared).
  transpilePackages: ['@smm/ui'],
};

export default nextConfig;
