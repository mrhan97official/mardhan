const withPWA = require("next-pwa")({
  dest: "public",
  register: true,
  skipWaiting: true,
  disable: process.env.NODE_ENV === "development",
  runtimeCaching: [
    {
      urlPattern: /\/api\/branding\/icon(?:\?|$)/,
      handler: "NetworkFirst",
      options: { cacheName: "branding-icons-v25", expiration: { maxEntries: 12, maxAgeSeconds: 604800 } },
    },
    {
      urlPattern: /\/manifest\.json(?:\?|$)/,
      handler: "NetworkFirst",
      options: { cacheName: "branding-manifest-v25" },
    },
    {
      urlPattern: /\/api\/.*$/,
      handler: "NetworkOnly",
      options: {},
    },
    {
      urlPattern: /^(?!.*\/api\/).*/,
      handler: "StaleWhileRevalidate",
      options: { cacheName: "app-shell-v25" },
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
