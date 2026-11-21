# The access-presence identity (docs/spec/persistence-api.md §3.3 "Presence"): read the metadata of
# each cluster's Talos os CA key, to say whether a version is stored. The metadata path never
# returns the key. No data path, no list, no write, no delete, no other secret, no Transit.
# bin/up writes this file as bw-presence; internal/baotest writes it under a test name.
path "secret/metadata/access/talos-ca/*" {
  capabilities = ["read"]
}
