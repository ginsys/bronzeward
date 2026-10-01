# The compiler identity (docs/spec/compilation.md §1): read pinned secret versions, encrypt under
# the artifact key. No decryption, no secret creation. bin/up writes this file as bw-compiler;
# internal/baotest writes it under a test name.
path "secret/data/gen/*" {
  capabilities = ["read"]
}

path "transit/encrypt/bw-artifact" {
  capabilities = ["update"]
}
