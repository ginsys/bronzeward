# The fixture's metadata-only identity: KV metadata and Transit key state, no value. Its list
# grant is the fixture's, not dependency-monitor.md choice §11.9's read-only one. bin/up writes
# this file as bw-metadata-only; internal/baotest writes it under a test name.
path "secret/metadata/*" { capabilities = ["read", "list"] }
path "transit/keys/*"    { capabilities = ["read", "list"] }
