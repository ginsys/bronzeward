# The compiler identity (docs/spec/compilation.md §1): read pinned secret versions, read a cluster's
# CA key at the signer's path to render the typed reference (choice §16.40), encrypt under the
# artifact key. No decryption, no secret creation; no list, metadata, write or delete on the signer's
# path. bin/up writes this file as bw-compiler; internal/baotest writes it under a test name.
path "secret/data/gen/*" {
  capabilities = ["read"]
}

path "secret/data/access/talos-ca/*" {
  capabilities = ["read"]
}

path "transit/encrypt/bw-artifact" {
  capabilities = ["update"]
}
