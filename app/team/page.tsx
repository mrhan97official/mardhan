"use client";

import AppShell from "@/components/AppShell";
import MembersManager from "@/components/MembersManager";
import { useSession } from "@/lib/session";

export default function TeamPage() {
  const { role } = useSession();

  return (
    <AppShell title="Member & Akses" subtitle="Siapa boleh masuk dan apa yang boleh dilakukan" isOffline={false}>
      {role === "owner" ? <MembersManager /> : (
        <section className="card p-2 text-sm text-slate-400">Halaman ini hanya untuk owner.</section>
      )}
    </AppShell>
  );
}
