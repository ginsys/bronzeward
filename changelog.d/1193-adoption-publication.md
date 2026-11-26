Fixed
- A publication is now refused as stale input when another publication gave a covered machine its
  first assignment after it was compiled. Before, it could remove a fragment that assignment selects
  and drop the assignment from the machine's desired configuration. The publication side of adopting
  and dropping a shared fragment or profile is in place; no API route writes either yet.
  Refs ginsys/bronzeward#1193.
