Fixed
- A fragment keeps the layer it was published in: an update in another layer, and the publication
  of one, are refused. Before, the update was accepted and its publication failed on a database
  constraint. Refs ginsys/bronzeward#1193.
