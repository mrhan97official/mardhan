export default function AppsPromoBanner() {
  return (
    <section className="relative overflow-hidden rounded-2xl border border-accent-blue/20 bg-gradient-to-r from-blue-500/10 via-base-900 to-violet-500/10 p-5 shadow-lg">
      <div className="relative z-10 max-w-2xl">
        <p className="text-xs font-semibold uppercase tracking-wider text-accent-blue">Application Center</p>
        <h2 className="mt-1 text-xl font-bold text-white">Kelola dan promosikan aplikasi Anda</h2>
        <p className="mt-2 text-sm text-slate-400">
          Temukan aplikasi yang tersedia, pantau deployment, dan akses layanan langsung dari halaman aplikasi.
        </p>
      </div>
      <div className="pointer-events-none absolute -right-8 -top-10 h-40 w-40 rounded-full bg-blue-400/10 blur-3xl" />
    </section>
  );
}
