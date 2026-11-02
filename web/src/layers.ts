// Composition order, global first (#257 B17; persistence-api §9.3). Kept apart from the draft page,
// which the profile and assignment panes import and which imports them.
export const LAYERS = ["global", "site", "cluster", "role", "workload", "override"] as const;
