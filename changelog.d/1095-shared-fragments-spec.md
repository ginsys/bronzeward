Added
- The specification now covers fragments and profiles shared between clusters. A shared change creates a new
  revision and reaches no cluster by itself: each cluster adopts it in a draft of its own and sees it in its own
  plan diff, and a list shows every cluster still on an older revision. Refs ginsys/bronzeward#1095.
