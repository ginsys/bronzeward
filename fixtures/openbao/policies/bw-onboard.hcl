# The onboarding operator's token for `bronzewardctl cluster onboard`: create-only write of each
# cluster's Talos access credential (docs/spec/persistence-api.md §3.3). The one capability is
# create on the access path. No read, so the token cannot read back what it wrote or any other
# cluster's credential; no update, so a cas=0 create cannot replace a version; no delete, list or
# patch of the path; no metadata, destroy or undelete path; nothing outside access/talos. The
# server's ingestion identity reads the secret (bw-ingestion.hcl). Written under a test name by
# internal/baotest; the operator writes it as bw-onboard and issues the token themselves.
path "secret/data/access/talos/*" {
  capabilities = ["create"]
}
