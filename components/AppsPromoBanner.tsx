"use client";

import { useEffect, useState } from "react";
import { ExternalLink } from "lucide-react";
import type { ProjectEntry } from "@/lib/projectRepos";
import { useOfflineData } from "@/lib/useOfflineData";

interface Promotion {
  repo: string;
  app_name: string;
  app_url: string;
  title: string;
  description: string;
  image_repo: string;
  version: string;
}

export default function AppsPromoBanner({ apps }: { apps: ProjectEntry[] }) {
  const promotion = useOfflineData<Promotion | null>("app-promo-v1", "/api/app-promo", null, 10000);
  const [imageFailed, setImageFailed] = useState(false);
  useEffect(() => { setImageFailed(false); }, [promotion.data?.image_repo, promotion.data?.version]);

  const banner = promotion.data;
  const legacyApp = banner?.repo ? apps.find((item) => item.repo.full_name.toLowerCase() === banner.repo.toLowerCase()) : undefined;
  const appName = banner?.app_name || (banner?.repo ? legacyApp?.repo.name || banner.repo.split("/").pop() : "");
  const appURL = banner?.app_url || legacyApp?.service?.app_url || legacyApp?.repo.html_url;
  const imageURL = banner
    ? `/api/project-thumbnails?repo=${encodeURIComponent(banner.image_repo)}&v=${encodeURIComponent(banner.version)}`
    : "";
  const hasCaption = !!(banner?.title || banner?.description || appName || appURL);

  return (
    <div className="mx-auto w-full max-w-[1440px] space-y-2">
      <section className="overflow-hidden rounded-2xl border border-base-border bg-white shadow-sm">
        {banner ? (
          <div className="relative aspect-[16/5] w-full bg-white">
            {!imageFailed ? (
              // The saved image is 1440 × 450; no dark filter or overlay covers it.
              // eslint-disable-next-line @next/next/no-img-element
              <img key={imageURL} src={imageURL} alt={banner.title || appName || "Banner promosi"} className="h-full w-full object-cover" onError={() => setImageFailed(true)} />
            ) : <p role="alert" className="flex h-full items-center justify-center text-sm text-slate-600">Gambar banner belum dapat ditampilkan.</p>}
          </div>
        ) : (
          <div className="flex min-h-[150px] items-center justify-center bg-gradient-to-r from-sky-50 to-white px-5 text-center text-slate-700 sm:aspect-[16/5]">
            <div>
              <p className="text-base font-semibold">Banner promosi belum diatur</p>
              <a href="/settings#promo-banner" className="mt-2 inline-block text-sm font-medium text-blue-700 underline underline-offset-2">Atur banner di Pengaturan</a>
            </div>
          </div>
        )}
        {banner && hasCaption && (
          <div className="flex flex-wrap items-center justify-between gap-4 border-t border-slate-200 bg-slate-50 px-4 py-3 sm:px-6">
            <div className="min-w-0">
              {appName && <p className="text-xs font-semibold uppercase tracking-wide text-blue-700">{appName}</p>}
              {banner.title && <h2 className="mt-0.5 text-lg font-semibold text-slate-900 sm:text-xl">{banner.title}</h2>}
              {banner.description && <p className="mt-1 text-sm text-slate-600">{banner.description}</p>}
            </div>
            {appURL && <a href={appURL} target="_blank" rel="noopener noreferrer" className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700">
              {appName ? "Buka aplikasi" : "Selengkapnya"} <ExternalLink size={15} />
            </a>}
          </div>
        )}
      </section>
      {promotion.error && <p role="alert" className="text-xs text-amber-300">Banner belum dapat diperbarui: {promotion.error}</p>}
    </div>
  );
}
