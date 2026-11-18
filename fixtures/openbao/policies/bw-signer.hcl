# The signer identity (docs/spec/persistence-api.md §3.4): read and write each cluster's Talos os CA
# key. `create` and `update` are both needed, because KV v2 treats a write to an existing path as an
# update, which a replaced key is. No list, no delete, no metadata path, no other secret, no
# Transit. bin/up writes this file as bw-signer; internal/baotest writes it under a test name.
path "secret/data/access/talos-ca/*" {
  capabilities = ["create", "read", "update"]
}
