# The orphan-report identity (docs/spec/persistence-api.md §6.4, choice §17.30): list the
# generation tree and read a generation's metadata, nothing else. No value, no other metadata
# path, no Transit, no write or delete of any kind. bin/up writes this file as bw-orphan-report;
# internal/baotest writes it under a test name.
path "secret/metadata/gen/*" { capabilities = ["read", "list"] }
