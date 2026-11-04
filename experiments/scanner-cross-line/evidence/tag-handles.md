oracle module: go.yaml.in/yaml/v3 v3.0.5
server probe module: go.yaml.in/yaml/v3 v3.0.5
main = web/src/position.ts, last changed at 3e00e342. before = web/src/position.ts at 1cf21917.

| case | source | go-yaml | server | main | before |
|---|---|---|---|---|---|
| verbatim-local | issue 603, lost | x/foo | accepted | none (lost) | none (lost) |
| verbatim-long-form | added | none | refused | none (=) | none (=) |
| verbatim-in-flow | added | x/foo | accepted | none (lost) | none (lost) |
| verbatim-with-handle-directive | added | x/foo | accepted | none (lost) | none (lost) |
| directive-handle-to-primary | issue 603, lost | x/foo | accepted | x/foo (=) | x/foo (=) |
| directive-secondary-to-primary | issue 603, lost | x/foo | accepted | x/foo (=) | x/foo (=) |
| directive-yaml-version | issue 603, lost | x/foo | accepted | x/foo (=) | x/foo (=) |
| directive-yaml-version-12 | added | refused | refused | extra x/foo | extra x/foo |
| directive-yaml-and-tag | issue 603, lost | x/foo | accepted | x/foo (=) | x/foo (=) |
| directive-handle-in-flow | added | x/foo | accepted | x/foo (=) | x/foo (=) |
| directive-handle-on-root-scalar | added | x/foo | refused | x/foo (=) | x/foo (=) |
| directive-after-document-end | issue 603, lost | x/foo | accepted | x/foo (=) | x/foo (=) |
| directive-two-documents-reset | issue 603 | refused | refused | extra x/foo | extra x/foo |
| directive-two-documents-redefined | added | x/foo | refused | x/foo, x/bar (extra) | x/foo, x/bar (extra) |
| directive-two-documents-each-own | added | x/foo, x/bar | accepted | x/foo, x/bar (=) | x/foo, x/bar (=) |
| directive-in-literal-text | added | refused | refused | extra x/foo | extra x/foo |
| directive-in-comment | added | refused | refused | extra x/foo | extra x/foo |
| directive-unknown | added | refused | refused | extra x/foo | extra x/foo |
| handle-undeclared | added | refused | refused | extra x/foo | extra x/foo |
| handle-undeclared-two-bangs | added | refused | refused | extra x/foo | extra x/foo |
| secondary-without-directive | issue 603, extra | none | refused | x/foo (extra) | x/foo (extra) |
| secondary-without-directive-in-flow | added | none | refused | x/foo (extra) | x/foo (extra) |
| primary-redefined | issue 603, extra | none | refused | x/foo (extra) | x/foo (extra) |
| named-handle-redefined-local | added | none | refused | x/foo (extra) | x/foo (extra) |
| named-handle-to-uri | added | none | refused | x/foo (extra) | x/foo (extra) |
| secondary-to-uri | added | none | refused | x/foo (extra) | x/foo (extra) |
| primary-to-core-uri | added | none | refused | x/foo (extra) | x/foo (extra) |
| primary-redefined-then-reset | added | refused | refused | extra x/foo | extra x/foo |
| secondary-core-redefined | added | x/bar | refused | x/bar (=) | x/bar (=) |
| handle-prefix-splits-name | added | x/foo | accepted | none (lost) | none (lost) |
| primary-prefix-splits-name | added | x/foo | accepted | none (lost) | none (lost) |
| secondary-prefix-splits-name | added | x/foo | accepted | none (lost) | none (lost) |
| handle-prefix-is-whole-name | added | refused | refused | = | = |
| percent-escape-plain | added | x/foo | accepted | none (lost) | none (lost) |
| percent-escape-verbatim | added | x/foo | accepted | none (lost) | none (lost) |
| percent-escape-named-handle | added | x/foo | accepted | none (lost) | none (lost) |
| tag-then-anchor | found by S8 | x/foo | accepted | none (lost) | none (lost) |
| tag-then-anchor-next-line | added | x/foo | accepted | none (lost) | none (lost) |
| tag-then-anchor-quoted | added | x/foo | accepted | none (lost) | none (lost) |
| anchor-then-tag | control | x/foo | accepted | x/foo (=) | x/foo (=) |
| tagged-key | found by S8 | x/foo | refused | none (lost) | none (lost) |
| plain-reference | control | x/foo | accepted | x/foo (=) | x/foo (=) |
| plain-reference-two-documents | control | x/foo, x/bar | accepted | x/foo, x/bar (=) | x/foo, x/bar (=) |
| family-b-flow-seq-fixed | 603 comment, fixed | x/foo | refused | x/foo (=) | x/foo (=) |
| family-b-flow-map-fixed | 603 comment, fixed | x/foo | refused | x/foo (=) | x/foo (=) |
| family-b-flow-seq-old | 603 comment, old | refused | refused | = | extra x/foo |
| family-b-flow-map-old | 603 comment, old | refused | refused | = | extra x/foo |
