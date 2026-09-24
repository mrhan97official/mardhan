"use client";

import { Settings } from "lucide-react";
import AppShell from "@/components/AppShell";
import ComingSoon from "@/components/ComingSoon";

export default function SettingsPage() {
  return (
    <AppShell title="Settings" subtitle="Workspace and account preferences">
      <ComingSoon
        icon={Settings}
        title="Pengaturan segera hadir"
        description="Halaman Settings sudah siap, tinggal dihubungkan ke API konfigurasi saat tersedia."
      />
    </AppShell>
  );
}
