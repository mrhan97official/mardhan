const withPWA = require("next-pwa")({
  dest: "public",
  register: true,
  skipWaiting: true,
  disable: process.env.NODE_ENV === "development",
  runtimeCaching: [
    {
      urlPattern: /\/api\/branding\/icon(?:\?|$)/,
      handler: "NetworkFirst",
      options: { cacheName: "branding-icons-v28", expiration: { maxEntries: 12, maxAgeSeconds: 604800 } },
    },
    {
      urlPattern: /\/manifest\.json(?:\?|$)/,
      handler: "NetworkFirst",
      options: { cacheName: "branding-manifest-v28" },
    },
    {
      urlPattern: /\/api\/.*$/,
      handler: "NetworkOnly",
      options: {},
    },
    {
      urlPattern: /^(?!.*\/api\/).*/,
      handler: "StaleWhileRevalidate",
      options: { cacheName: "app-shell-v28" },
    },
  ],
  fallbacks: {
    document: "/offline",
  },
});

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  eslint: { ignoreDuringBuilds: true },
};

module.exports = withPWA(nextConfig);
