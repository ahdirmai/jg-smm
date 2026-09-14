/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',
  reactStrictMode: true,
  // Consume workspace packages that ship TypeScript source (ui) or built dist (shared).
  transpilePackages: ['@smm/ui'],
};

export default nextConfig;
