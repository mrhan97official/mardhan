import { WifiOff } from "lucide-react";

export default function OfflinePage() {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-4 bg-base-950 px-6 text-center">
      <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-amber-400/15 text-amber-400">
        <WifiOff size={26} />
      </div>
      <h1 className="text-xl font-bold text-white">Kamu sedang offline</h1>
      <p className="max-w-sm text-sm text-slate-400">
        DevControl belum bisa memuat halaman ini karena belum pernah dibuka sebelumnya.
        Data yang sudah tersimpan tetap bisa diakses dari halaman utama.
      </p>
      <a
        href="/"
        className="rounded-xl bg-accent-blue px-4 py-2 text-sm font-semibold text-white hover:bg-blue-500"
      >
        Kembali ke Overview
      </a>
    </div>
  );
}
