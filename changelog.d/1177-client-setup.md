Changed
- The clients meet the setup rules of the client contract. A CLI context that signs in needs `oidc.scopes` and
  `oidc.audience`: `context add` stores `openid offline_access` when no `--scopes` is given and needs `--audience`
  (or a client configuration file that names one). A contexts file that already holds an OIDC context without
  them still loads and `context list`, `use` and `remove` still work, but every command that opens that context
  refuses with the missing field named and the line to add, before any request. The CLI now needs API revision
  10 or later for every command, and an interactive write prints its context before it sends. The web console
  shows a start mode it does not know as unknown, and `ingestWaitSeconds` in `bronzeward-client.json` sets
  how long it waits for an in-progress import. Fixes ginsys/bronzeward#1177.
