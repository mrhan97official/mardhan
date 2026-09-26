import type { LucideIcon } from "lucide-react";

export default function ComingSoon({
  icon: Icon,
  title,
  description,
}: {
  icon: LucideIcon;
  title: string;
  description: string;
}) {
  return (
    <div className="card flex flex-col items-center justify-center gap-2 px-2 py-2 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-accent-blue/15 text-accent-blue">
        <Icon size={22} />
      </div>
      <h2 className="text-base font-semibold text-white">{title}</h2>
      <p className="max-w-sm text-sm text-slate-400">{description}</p>
    </div>
  );
}
