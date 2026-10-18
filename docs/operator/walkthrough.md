# Walkthrough: adoption to drift

One existing Talos cluster taken through the PoC's workflow on the fixture: record and import it,
adopt both machines, publish a change, approve and apply it to the worker, then detect, adopt and
revert drift. Each section is one stage of the [acceptance plan](../spec/acceptance-plan.md)
(S0-S5). The scenario scripts under [`fixtures/scenarios/`](../../fixtures/scenarios) are the
executable record of these steps, with every check and negative control added; this page keeps
only the path an operator takes. Failure, interruption and restoration are not covered here.

Start from [Start the fixture](README.md#start-the-fixture). Every command runs from the repository
root.

## Setup for the commands below

```sh
set -a; . fixtures/versions.env; set +a
A=http://127.0.0.1:$SERVER_A_PORT
H=fixtures/.state/walk-headers B=fixtures/.state/walk-body

# mint <human>: a five-minute token from the fixture issuer's key. Mint again when one expires.
mint() {
  docker run --rm --pull never --network none --user "$(id -u):$(id -g)" \
    -v "$PWD/fixtures/.state/issuer:/etc/oidc:ro" --entrypoint /oidc "$(cat fixtures/.state/up-image)" \
    mint -key /etc/oidc/key.json -issuer http://issuer:5556 -human "$1"
}
# call <method> <path> <token> [body] [idempotency key] [etag]: the response body on stdout.
# The token goes over stdin and the body through a file only you can read, never on curl's
# command line. The file is removed once curl returns, whether or not it succeeded.
call() {
  local args=(-sS -D "$H" -X "$1" -H @-) rc=0
  if [ -n "${4:-}" ]; then
    (umask 077 && printf '%s' "$4" >"$B")
    args+=(-H 'Content-Type: application/json' --data-binary "@$B")
  fi
  [ -z "${5:-}" ] || args+=(-H "Idempotency-Key: $5")
  [ -z "${6:-}" ] || args+=(-H "If-Match: $6")
  printf 'Authorization: Bearer %s\n' "$3" | curl "${args[@]}" "$A/api/v1$2" || rc=$?
  rm -f -- "$B"
  return "$rc"
}
etag() { sed -n 's/^[Ee][Tt][Aa][Gg]: *//p' "$H" | tr -d '\r'; }

author=$(mint h-author) publisher=$(mint h-publisher) approver=$(mint h-approver) viewer=$(mint h-viewer)
```

Every write carries an `Idempotency-Key`: sending the same request again with the same key
returns the first answer instead of acting twice. Every change to a draft carries `If-Match` with
the draft's current ETag; a stale one gets `412` and nothing is written.

*On your deployment:* tokens come from your identity provider, and `A` is your server's address.
`call` keeps the token and the body off the command line, which any local user can read; keep that
when you adapt it, since a draft body can carry values that become secrets.

## S0: the empty installation

```sh
curl -i $A/livez                                   # 204
call GET /acts "$viewer"                           # 200: a viewer reads
call GET /acts "$(cat fixtures/.state/automation-token)"   # 200: the automation identity
```

The automation identity `fixtures/bin/seed` issued holds `author` and `publisher`. It cannot reach
a human-only route (cluster and machine records, ingestion, approval); those answer `403`.

## S1: record the cluster and import its machines

**1. Read the identities from the nodes.** Bronzeward identifies a cluster and its machines by the
IDs Talos reports, never by name:

```sh
export TALOSCONFIG=$PWD/fixtures/.state/talosconfig
T=fixtures/.cache/talosctl   # the pinned talosctl up downloaded (the fixture re-checks its digest; this does not)
cid=$($T -e $TALOS_CONTROLPLANE_IP -n $TALOS_CONTROLPLANE_IP get info -o json | jq -r .spec.clusterId)
nid_cp=$($T -e $TALOS_CONTROLPLANE_IP -n $TALOS_CONTROLPLANE_IP get identity -o json | jq -r .spec.nodeId)
nid_w=$($T -e $TALOS_WORKER_IP -n $TALOS_WORKER_IP get identity -o json | jq -r .spec.nodeId)
```

**2. Record the cluster and both machines** as `h-author`:

```sh
cluster=$(call POST /clusters "$author" "$(jq -nc --arg c "$cid" --arg e "https://$TALOS_CONTROLPLANE_IP:6443" \
  '{name: "bw-fixture", endpoint: $e, contract: "v1.13", talosClusterId: $c}')" walkthrough-cluster | jq -r .id)
machine_cp=$(call POST /machines "$author" "$(jq -nc --arg c "$cluster" --arg n "$nid_cp" --arg e "$TALOS_CONTROLPLANE_IP:50000" \
  '{cluster: $c, talosNodeId: $n, platform: "container", talosEndpoint: $e}')" walkthrough-machine-cp | jq -r .id)
machine_w=$(call POST /machines "$author" "$(jq -nc --arg c "$cluster" --arg n "$nid_w" --arg e "$TALOS_WORKER_IP:50000" \
  '{cluster: $c, talosNodeId: $n, platform: "container", talosEndpoint: $e}')" walkthrough-machine-w | jq -r .id)
```

`talosEndpoint` is an IP address with an optional port, never a DNS name.

**3. Give Bronzeward the cluster's Talos access**, as the OpenBao administrator. The value goes
straight to OpenBao; no Bronzeward request carries it:

```sh
root=$(sed -n 's/^BW_BAO_ROOT_TOKEN=//p' fixtures/.state/secrets.env)
BAO_TOKEN=$root docker exec -i -e BAO_TOKEN bw-fixture-openbao \
  bao kv put -mount=secret "access/talos/$cluster" talosconfig=- <"$TALOSCONFIG"
```

**4. Import each machine into a draft.** An import reads the node's configuration, stores every
secret in it as a create-only OpenBao generation, and leaves a `!bwref` reference in its place.
`marks` names values that are secret but that Talos's schema does not mark as such; here the
worker's planted file content:

```sh
draft=$(call POST /drafts "$author" "$(jq -nc --arg c "$cluster" '{cluster: $c, title: "S1 import"}')" walkthrough-draft | jq -r .id)
etag=$(etag)
call POST /ingestions "$author" "$(jq -nc --arg m "$machine_cp" --arg d "$draft" \
  '{kind: "import", source: "machine", staging: "transient", machine: $m, draft: $d, marks: []}')" walkthrough-import-cp "$etag"
```

The answer is `202` with `ingestion` and `operation`. Poll `GET /ingestions/<ingestion>` until
`state` is `released`, check `GET /operations/<operation>` reads `succeeded`, then read the draft's
new ETag with `call GET /drafts/$draft "$author" >/dev/null; etag=$(etag)`. Import the worker the
same way, with `machine_w`, `marks: ["doc[0]/machine/files/0/content"]` and its own key,
`walkthrough-import-w`: a key sent again with a different request is refused
`422 idempotency-key-reused`.

Bronzeward never writes to a node during an import. S1 shows this by comparing each node's
configuration digest and apid log before and after.

*On your deployment:* `platform` and the endpoints are your machines'; write your own talosconfig
with `bao kv put -mount=secret access/talos/<cluster id> talosconfig=@<file>`.

## S3 handover: adopt both machines

Adoption makes the imported configuration the machine's `Applied` state without sending anything to
the node. It needs a published release covering the machine and an assignment selecting something.

**1. In the import draft**, as `h-author`, add a cluster fragment and assign it to both machines:

```sh
call PUT /drafts/$draft/fragments/s3-handover "$author" \
  "$(jq -nc '{layer: "cluster", document: "machine:\n  nodeLabels:\n    bw-s3-handover: \"yes\"\n", marks: []}')" \
  walkthrough-fragment-handover "$etag"; etag=$(etag)
for m in "$machine_cp" "$machine_w"; do
  call PUT /drafts/$draft/assignments/$m "$author" '{"profiles": [], "fragments": {"cluster": ["s3-handover"]}}' \
    "walkthrough-assignment-$m" "$etag"; etag=$(etag)
done
```

**2. Publish** as `h-publisher`. Publication compiles every machine's configuration and encrypts
it; the answer is `202` with the publication operation's `id`:

```sh
pub=$(call POST /drafts/$draft/publications "$publisher" '{}' walkthrough-publish-import "$etag" | jq -r .id)
call GET /operations/$pub "$viewer" | jq -r .state      # until succeeded
release=$(call GET /machines/$machine_w "$viewer" | jq -r .desired)
```

**3. Plan and approve an adoption per machine.** A `publisher` creates the plan; a different human
holding `approver` approves it:

```sh
for m in "$machine_cp" "$machine_w"; do
  plan=$(call POST /plans "$publisher" "$(jq -nc --arg r "$release" --arg m "$m" \
    '{releaseId: $r, machine: $m, operation: "adopt"}')" "walkthrough-adopt-$m" | jq -r .id)
  call POST /plans/$plan/approvals "$approver" '{}' "walkthrough-adopt-approval-$m"
done
```

The approval answers before the adoption is recorded. The controller then observes the node,
checks its configuration digest is still the imported one, and records the adoption. Wait for both
before S2: a change to an assignment the plan bound, made first, gets the adoption refused.

```sh
for m in "$machine_cp" "$machine_w"; do
  call GET /machines/$m "$viewer" | jq -r '.applied.source'      # until adoption
done
```

`GET /machines/<id>` then shows `desired` and `applied.release` as the release, with
`applied.source` `adoption`. If the node changed since the import, the adoption is refused and the
plan stays approved.

## S2: change the worker's configuration

**1. A new draft with a fragment that references a secret.** A fragment never holds a secret
value. It holds a `!bwref` to one, declared with its exact version. Read the reference the
adopted release recorded for the worker's file content:

```sh
review=$(call GET /releases/$release/machines/$machine_w/review "$author")
ref=$(jq -r '.provenance[] | select(.source.path == "doc[0]/machine/files/0/content") | .reference' <<<"$review")
ver=$(jq -r '.provenance[] | select(.source.path == "doc[0]/machine/files/0/content") | .version' <<<"$review")

draft=$(call POST /drafts "$author" "$(jq -nc --arg c "$cluster" '{cluster: $c, title: "S2 change"}')" walkthrough-draft-s2 | jq -r .id)
etag=$(etag)
frv=$(call PUT /drafts/$draft/fragments/s2-labels "$author" "$(printf 'machine:\n  nodeLabels:\n    bw-s2: !bwref %s\n' "$ref" |
  jq -Rsc --arg n "$ref" --argjson v "$ver" '{layer: "role", document: ., marks: [],
    declarations: {references: {($n): {kind: "string", version: $v}}}}')" walkthrough-fragment-s2 "$etag" | jq -r .entry.revision)
etag=$(etag)
```

**2. A profile listing the fragment revision, and the worker's assignment selecting it:**

```sh
call PUT /drafts/$draft/profiles/workers "$author" "$(jq -nc --arg f "$frv" '{fragments: [$f]}')" walkthrough-profile "$etag"; etag=$(etag)
call PUT /drafts/$draft/assignments/$machine_w "$author" '{"profiles": ["workers"], "fragments": {}}' walkthrough-assignment-s2 "$etag"; etag=$(etag)
```

**3. Publish**, as in the handover. The release becomes the worker's `desired`; `applied` stays the
adopted release until a plan runs.

```sh
pub=$(call POST /drafts/$draft/publications "$publisher" '{}' walkthrough-publish-s2 "$etag" | jq -r .id)
call GET /operations/$pub "$viewer" | jq -r .state      # until succeeded
release=$(call GET /machines/$machine_w "$viewer" | jq -r .desired)
```

**4. Review what was published.** The review shows the label as its reference, never the value,
with its provenance naming the fragment revision. The release names its source revisions and
the exact renderer:

```sh
call GET /releases/$release/machines/$machine_w/review "$author" | jq .provenance
call GET /releases/$release "$author" | jq .sources
call GET /dependencies "$viewer" | jq '.items[] | {id, class, stale}'    # every dependency retained
```

The automation token can do steps 1-3 as well; it holds `author` and `publisher`.

## S3: plan and approve the apply

As `h-publisher`, plan applying the worker's `desired` release without a reboot. Values left out
take the configuration's `planDefaults`:

```sh
plan=$(call POST /plans "$publisher" "$(jq -nc --arg r "$release" --arg m "$machine_w" \
  '{releaseId: $r, machine: $m, operation: "apply-config", mode: "no-reboot", expiresInSeconds: 7200,
    maxObservationAgeSeconds: 300, checkValiditySeconds: 600, transportDeadlineSeconds: 120,
    verificationDeadlineSeconds: 300, maxAttempts: 2}')" walkthrough-plan-apply | jq -r .id)
call GET /plans/$plan "$viewer" | jq '{state, evidence}'   # proposed
call POST /plans/$plan/approvals "$approver" '{}' walkthrough-approval-apply
```

The approval names the plan, the approver, the role and the installation epoch. `selfApproval`
marks an approver who contributed to the release, with the reasons. Until the controller commits
the plan, the `publisher` who created it, an `approver` or a `recovery-admin` can cancel it
(`POST /plans/<id>/cancellations`), and an `approver` or a `recovery-admin` can revoke the approval
(`POST /approvals/<id>/revocations`). The `s3 run` script holds the plan here by cutting each
instance's link to the worker; this walkthrough lets it run.

## S4: the safe apply

Nothing more is sent by hand. The controller checks the plan again at use time, takes a fresh
evidence observation of the worker, commits the operation, sends `ApplyConfiguration` and
verifies the result by observing the node again:

```sh
call GET /machines/$machine_w "$viewer" | jq '{desired, applied, observed}'
```

Once `applied.release` is the release and `applied.source` is `operation`, the apply is done. The
operation, its attempts and the worker's timeline are served:

```sh
call GET "/machines/$machine_w/timeline?limit=500" "$viewer" | jq '.items[] | {kind, at}'
call GET "/machines/$machine_w/observations?limit=500" "$viewer"
```

A page with `next` continues at `&cursor=<next>`. The control plane receives nothing.

## S5: drift, adopted or reverted

The fixture's instances observe each machine every 168 hours. To see drift in minutes, set
`driftInterval: 5s` in `fixtures/.state/server/{a,b,c}/config.yaml`, then
`fixtures/bin/inject kill A` and `start A`, and likewise for B and C. Set it back the same way
afterwards. A deployment's default is five minutes.

**1. Change the worker out of band**, as an operator with `talosctl` would:

```sh
printf 'machine:\n  registries:\n    config:\n      registry.s5.invalid:\n        auth:\n          username: bw-s5\n          password: BWSYNTH-walkthrough\n' >fixtures/.state/walk-patch
$T -e $TALOS_WORKER_IP -n $TALOS_WORKER_IP patch machineconfig --mode no-reboot --patch @fixtures/.state/walk-patch
rm fixtures/.state/walk-patch
```

Within a minute `GET /machines/$machine_w` shows `openDrift`: the timeline revision that opened it,
the observation that opened it, the latest observation recorded on it and a possible late landing.
The opening entry is on the timeline.

**2. Freeze the machine** while you decide. Any of `author`, `publisher`, `approver` or
`recovery-admin` may freeze; only an `approver` unfreezes:

```sh
call POST /machines/$machine_w/freezes "$author" '{"reason": "S5 drift"}' walkthrough-freeze
```

**3a. Adopt the drift.** Ingest the drifted configuration into a new draft as a `drift-adoption`,
marking every secret in it that the schema does not mark: the file content, S2's label (which
carries the same value) and the new password. A copy of a marked value left unmarked is refused:

```sh
draft=$(call POST /drafts "$author" "$(jq -nc --arg c "$cluster" '{cluster: $c, title: "S5 adopt"}')" walkthrough-draft-s5 | jq -r .id)
etag=$(etag)
call POST /ingestions "$author" "$(jq -nc --arg m "$machine_w" --arg d "$draft" '{kind: "drift-adoption", source: "machine",
  staging: "transient", machine: $m, draft: $d, marks: ["doc[0]/machine/files/0/content",
  "doc[0]/machine/nodeLabels/bw-s2", "doc[0]/machine/registries/config/registry.s5.invalid/auth/password"]}')" walkthrough-adopt-drift "$etag"
```

Wait for it as in S1 and refresh `etag`. The adopted base now holds S2's label as a reference, and
a fragment overriding it would refuse publication, so the worker must stop selecting `s2-labels`.
Here the `workers` profile lists nothing else, and a profile lists at least one fragment, so point
the worker's assignment back at the handover's cluster fragment. Then publish, plan an adoption
and approve it:

```sh
call PUT /drafts/$draft/assignments/$machine_w "$author" '{"profiles": [], "fragments": {"cluster": ["s3-handover"]}}' \
  walkthrough-assignment-s5 "$etag"; etag=$(etag)
pub=$(call POST /drafts/$draft/publications "$publisher" '{}' walkthrough-publish-s5 "$etag" | jq -r .id)
release=$(call GET /machines/$machine_w "$viewer" | jq -r .desired)    # once $pub succeeded
plan=$(call POST /plans "$publisher" "$(jq -nc --arg r "$release" --arg m "$machine_w" \
  '{releaseId: $r, machine: $m, operation: "adopt"}')" walkthrough-plan-adopt-drift | jq -r .id)
call POST /plans/$plan/approvals "$approver" '{}' walkthrough-approval-adopt-drift
```

The plan binds the open drift record (`driftRecord`). The adoption commits even while frozen, since
it sends nothing. The record closes `adopted` and `applied.source` reads `adoption`.

**3b. Or revert it.** After another out-of-band change opens a new record, plan an `apply-config` of
the machine's `desired` release with S3's body, under new idempotency keys: a key reused with a
changed request is refused `422 idempotency-key-reused`. The approval answers
`unseenOverwrite: true`: the approver is overwriting a state no release recorded. A frozen machine
runs no apply-config, so the approver unfreezes it, and the revert then runs as S4 did and closes
the record `reverted`:

```sh
release=$(call GET /machines/$machine_w "$viewer" | jq -r .desired)
plan=$(call POST /plans "$publisher" "$(jq -nc --arg r "$release" --arg m "$machine_w" \
  '{releaseId: $r, machine: $m, operation: "apply-config", mode: "no-reboot", expiresInSeconds: 7200,
    maxObservationAgeSeconds: 300, checkValiditySeconds: 600, transportDeadlineSeconds: 120,
    verificationDeadlineSeconds: 300, maxAttempts: 2}')" walkthrough-plan-revert | jq -r .id)
call POST /plans/$plan/approvals "$approver" '{}' walkthrough-approval-revert | jq .unseenOverwrite
call POST /machines/$machine_w/unfreezes "$approver" '{"reason": "S5 revert"}' walkthrough-unfreeze
```

*On your deployment:* a real drift is whatever changed the node. Decide from the timeline whether
to adopt it into a release or revert it, and mark every secret the drifted configuration holds
before adopting. Stop selecting any fragment that overrides a value the adopted base now holds as
a reference; remove it from its profile when the profile keeps another fragment.

This stage departs from `fixtures/scenarios/s5 run` in one place. The script removes `s2-labels`
from the `workers` profile, which by then lists the fragments `s4 ds` added as well. This
walkthrough skips `s4 ds`, so `workers` lists only `s2-labels`, and a profile listing no fragment
is refused. It reassigns the worker instead. Until a reviewer has run this page on the fixture,
that step is unverified.

## Where next

- Every route, body and refusal: [persistence and API](../spec/persistence-api.md).
- What must be checked, and how: [acceptance plan](../spec/acceptance-plan.md).
- Clean up: `fixtures/bin/down`.
