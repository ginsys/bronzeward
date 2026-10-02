# The ingestion identity (docs/spec/compilation.md §1): create-only secret generations, encrypt
# under the baseline key, encrypt and decrypt under the staging key, HMAC under the digest key,
# and read of each cluster's Talos access credential (docs/spec/persistence-api.md §3.3). No read
# of a generation, no update (so a cas=0 create cannot replace a version), no write, list or
# delete of the access path, no other key. Every Transit path is exact:
# "transit/hmac/bw-digest/<algorithm>" is not granted, so the algorithm comes from the request
# body alone. bin/up writes this file as bw-ingestion; internal/baotest writes it under a test
# name.
path "secret/data/gen/*" {
  capabilities = ["create"]
}

path "secret/data/access/talos/*" {
  capabilities = ["read"]
}

path "transit/encrypt/bw-baseline" {
  capabilities = ["update"]
}

path "transit/encrypt/bw-staging" {
  capabilities = ["update"]
}

path "transit/decrypt/bw-staging" {
  capabilities = ["update"]
}

path "transit/hmac/bw-digest" {
  capabilities = ["update"]
}
