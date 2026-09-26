import type { Metadata, Viewport } from "next";
import DeploymentOverlayProvider from "@/components/deployment/DeploymentOverlayProvider";
import AuthGate from "@/components/AuthGate";
import ConfirmationCenter from "@/components/ConfirmationCenter";
import BrandingProvider from "@/components/BrandingProvider";
import ThemeProvider from "@/components/ThemeProvider";
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
    <html lang="id" className="dark" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: `try{if(localStorage.getItem("devcontrol-theme")==="light"){document.documentElement.classList.remove("dark");document.documentElement.classList.add("light");document.querySelector('meta[name="theme-color"]')?.setAttribute("content","#F8FCFF");document.querySelector('meta[name="apple-mobile-web-app-status-bar-style"]')?.setAttribute("content","default")}}catch(e){}` }} />
      </head>
      <body className="font-sans antialiased min-h-screen bg-base-950 text-slate-100">
        <ThemeProvider><AuthGate><BrandingProvider><DeploymentOverlayProvider>{children}<ConfirmationCenter /></DeploymentOverlayProvider></BrandingProvider></AuthGate></ThemeProvider>
      </body>
    </html>
  );
}
