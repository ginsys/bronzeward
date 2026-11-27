// What the editors' Save and Remove send for a shared (library) fragment or profile (operator experience §5.4,
// ginsys/bronzeward#1193): the scope member of the body, the scope query of a removal, and the key a new shared entry is
// found by once its Save named it. A leaf module: the panes of drafts.tsx and composition.tsx both use it.

/** The body member that makes a fragment or profile write the shared (library) entry; absent writes the cluster's own. */
export const scopeMember = (shared: boolean) => (shared ? ({ scope: "library" } as const) : {});

/** The query of a removal of the shared entry; the cluster's own entry takes none. */
export const scopeQuery = (shared: boolean) => (shared ? "?scope=library" : "");

/** The name a new entry's Save claims: a shared entry has its own key, apart from the cluster's entry of the name. */
export const claimedName = (name: string, shared: boolean) => (shared ? `${name}:library` : name);
