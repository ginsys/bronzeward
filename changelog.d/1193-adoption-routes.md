Added
- A cluster's draft can adopt a shared fragment or profile at any of its revisions, stop following
  it, or detach it into a copy of its own, by API:
  `PUT` and `DELETE /drafts/{id}/adoptions/{kind}/{name}` and `POST` on its `/detachments`. A later
  entry for the same shared name replaces the earlier one. The CLI and the web UI list the new
  entries and write none. Refs ginsys/bronzeward#1193.
