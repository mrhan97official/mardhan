import type { Metadata, Viewport } from "next";
import DeploymentOverlayProvider from "@/components/deployment/DeploymentOverlayProvider";
import AuthGate from "@/components/AuthGate";
import BrandingProvider from "@/components/BrandingProvider";
import "./globals.css";

export const metadata: Metadata = {
  title: "DevControl · Infrastructure & Deployment Workspace",
  description: "Offline-first infrastructure and deployment control center.",
  manifest: "/manifest.json",
  icons: {
    icon: "/api/branding/icon?size=favicon",
    apple: "/api/branding/icon?size=apple",
  },
  appleWebApp: {
    capable: true,
    statusBarStyle: "black",
    title: "DevControl",
  },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
  themeColor: "#080D17",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="id" className="dark">
      <body className="font-sans antialiased min-h-screen bg-base-950 text-slate-100">
        <AuthGate><BrandingProvider><DeploymentOverlayProvider>{children}</DeploymentOverlayProvider></BrandingProvider></AuthGate>
      </body>
    </html>
  );
}
