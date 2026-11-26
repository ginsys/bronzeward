Changed
- Publication handles a draft's shared (library) fragment and profile entries: it moves the shared
  head and the author's cluster's adoption, refuses a release that would compose two revisions of
  one fragment name, and refuses a name a cluster would hold both as its own and as shared. No
  route creates such an entry yet. A database made from an earlier pre-release schema is
  recreated. Refs ginsys/bronzeward#1193.
