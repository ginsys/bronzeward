# The executor identity (docs/spec/compilation.md §1): decrypt artifacts. No secret read, no write,
# no staging or baseline decryption. bin/up writes this file as bw-executor; internal/baotest
# writes it under a test name.
path "transit/decrypt/bw-artifact" {
  capabilities = ["update"]
}
