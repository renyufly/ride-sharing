import type { NextConfig } from "next";

const matcherApiUrl = process.env.MATCHER_API_URL || "http://matcher-api:8082";

const nextConfig: NextConfig = {
  images: {
    // Load images from randomuser.me for Lego driver profile pictures
    domains: ["randomuser.me"],
  },
  reactStrictMode: false,

  async rewrites() {
    return [
      {
        source: "/matcher-api/:path*",
        destination: `${matcherApiUrl}/:path*`,
      },
    ];
  },
};

export default nextConfig;
