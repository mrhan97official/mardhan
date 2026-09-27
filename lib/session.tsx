"use client";

import { createContext, useContext } from "react";

export type Role = "owner" | "admin" | "operator" | "viewer";
export type Session = { role: Role; name: string };

export const SessionContext = createContext<Session>({ role: "viewer", name: "" });

export function useSession(): Session {
  return useContext(SessionContext);
}

// UI mirror of the server permission matrix (pkg/auth/members.go). The
// server stays the authority; this only hides what a role cannot use.
export const isAdminRole = (role: Role) => role === "owner" || role === "admin";
export const canDeploy = (role: Role) => role !== "viewer";

export const ROLE_LABEL: Record<Role, string> = {
  owner: "Owner",
  admin: "Admin",
  operator: "Operator",
  viewer: "Viewer",
};
