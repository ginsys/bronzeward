Fixed
- A command that meets a failed system credential store now stops with the store's error before any sign-in starts, with or without `--no-input`, instead of starting a sign-in that lasts for one process. `login`, `logout` and `context remove` are unchanged. Refs ginsys/bronzeward#1240.
