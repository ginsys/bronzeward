# The executor identity (docs/spec/compilation.md §1): decrypt artifacts only. No secret read, no
# staging or baseline decryption. bin/up writes this file as bw-executor; internal/baotest writes
# it under a test name.
path "transit/decrypt/bw-artifact" {
  capabilities = ["update"]
}
