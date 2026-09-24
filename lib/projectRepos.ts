import type { GithubRepo, Service } from "./types";

export interface ProjectEntry {
  repo: GithubRepo;
  service?: Service;
  source: "github" | "stored";
}

// GitHub determines which repositories exist; D1 only supplies deployment
// metadata. Also show a stored link if the token loses access to its repo.
export function mergeProjects(repos: GithubRepo[], services: Service[]): ProjectEntry[] {
  const serviceByRepo = new Map<string, Service>();
  for (const service of services) {
    if (service.repo) serviceByRepo.set(service.repo.toLowerCase(), service);
  }

  const entries: ProjectEntry[] = [];
  const seen = new Set<string>();
  for (const repo of repos) {
    const key = repo.full_name.toLowerCase();
    if (seen.has(key)) continue;
    entries.push({ repo, service: serviceByRepo.get(key), source: "github" });
    seen.add(key);
  }

  for (const service of services) {
    if (!service.repo || seen.has(service.repo.toLowerCase())) continue;
    entries.push({
      repo: {
        full_name: service.repo,
        name: service.repo.split("/").pop() || service.name,
        default_branch: service.branch || "main",
        private: false,
      },
      service,
      source: "stored",
    });
    seen.add(service.repo.toLowerCase());
  }
  return entries;
}
