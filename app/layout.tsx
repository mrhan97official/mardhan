import type { Metadata, Viewport } from "next";
import DeploymentOverlayProvider from "@/components/deployment/DeploymentOverlayProvider";
import AuthGate from "@/components/AuthGate";
import "./globals.css";

export const metadata: Metadata = {
  title: "DevControl · Infrastructure & Deployment Workspace",
  description: "Offline-first infrastructure and deployment control center.",
  manifest: "/manifest.json",
  icons: {
    icon: "/icons/icon.svg",
    apple: "/icons/icon-192.png",
  },
  appleWebApp: {
    capable: true,
    statusBarStyle: "black-translucent",
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
        <AuthGate><DeploymentOverlayProvider>{children}</DeploymentOverlayProvider></AuthGate>
      </body>
    </html>
  );
}
