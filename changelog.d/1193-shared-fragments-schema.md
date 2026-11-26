Changed
- The database schema holds shared (library) fragments and profiles and each cluster's adoption of
  them, and a release compiles what its cluster has adopted. Nothing creates shared data yet. A
  database made from an earlier pre-release schema is recreated. Refs ginsys/bronzeward#1193.
