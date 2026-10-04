# The metadata identity (docs/spec/dependency-monitor.md §4, choice §11.9): read KV metadata and
# Transit key state by name, nothing else. No list, no value, no Transit operation, no key
# configuration. bin/up writes this file as bw-metadata; internal/baotest writes it under a test
# name.
path "secret/metadata/*" { capabilities = ["read"] }
path "transit/keys/*"    { capabilities = ["read"] }
