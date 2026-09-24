const withPWA = require("next-pwa")({
  dest: "public",
  register: true,
  skipWaiting: true,
  disable: process.env.NODE_ENV === "development",
  runtimeCaching: [
    {
      urlPattern: /^\/api\/.*$/,
      handler: "NetworkOnly",
      options: {},
    },
    {
      urlPattern: /^(?!.*\/api\/).*/,
      handler: "StaleWhileRevalidate",
      options: { cacheName: "app-shell-v18" },
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
