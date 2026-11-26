Added
- A draft's fragment and profile entries can be shared: `scope: library` on their `PUT`, and
  `?scope=library` on their `DELETE`. Publishing such an entry lets the draft's cluster use the
  shared fragment or profile; no other cluster can use it yet. A shared fragment holds no secret
  reference. Both clients show shared data and write none. Refs ginsys/bronzeward#1193.
