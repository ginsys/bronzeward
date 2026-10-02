# The executor identity (docs/spec/compilation.md §1): decrypt artifacts, and read each cluster's
# Talos access credential (docs/spec/persistence-api.md §3.3). No other secret read, no write,
# list or delete of the access path, no staging or baseline decryption. bin/up writes this file
# as bw-executor; internal/baotest writes it under a test name.
path "transit/decrypt/bw-artifact" {
  capabilities = ["update"]
}

path "secret/data/access/talos/*" {
  capabilities = ["read"]
}
