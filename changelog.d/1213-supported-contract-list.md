Changed
- The Talos contracts Bronzeward supports are an explicit list, apart from the version of the Talos
  library it is built with. The list holds v1.14 only, so adding a cluster, publishing and discovery
  behave as before. A listed contract newer than the library is still refused, and the Kubernetes
  version window is the cluster's contract's, not the library's. Refs ginsys/bronzeward#1213.
