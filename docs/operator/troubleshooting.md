# Troubleshooting

Start from the refusal, then read the machine's timeline. The commands use the
[walkthrough's setup](walkthrough.md#setup-for-the-commands-below).

## Reading a refusal

Every refusal is `application/problem+json`. Its `type` is `urn:bronzeward:problem:<code>`; `detail`,
and sometimes a `cause`, say which check refused. The response headers are in `$H`.

```sh
call POST /plans "$publisher" "$body" troubleshoot-plan-1 | jq '{type, status, detail, cause}'
```

| Code | Status | Usual cause | What to do |
|---|---|---|---|
| `idempotency-key-required` | 428 | A write without `Idempotency-Key` | Send one, unique per intended act |
| `invalid-request` | 400 | A malformed body or header, such as an `Idempotency-Key` outside 16 to 128 characters of `[A-Za-z0-9_-]`; `detail` names it | Fix the request |
| `idempotency-key-reused` | 422 | The same key with a different request | Use a new key; the same key replays only the same request |
| `precondition-required` | 428 | A draft change without `If-Match` | Send the draft's current ETag |
| `precondition-failed` | 412 | The draft moved since you read its ETag | Read the draft again, reapply your change |
| `stale-input` | 409 | A publication input moved since it was read | Publish again |
| `scope-busy` | 409 | An operation holds the machine's scope | Wait for it, or settle it ([recovery](recovery.md#interrupted-operations)) |
| `recovery-mode-active` | 409 | Recovery mode, or a scope not yet released | Finish [the restore](recovery.md#restoring-the-database-or-openbao) |
| `conflict` | 409 | A state the act does not apply to; `detail` names it | Read the object, then act on its current state |
| `identity-revoked` | 403 | The subject or service identity is revoked, or in `auth.deniedSubjects` | Revocation is permanent and a new token does not lift it: act as another human subject, or issue a new service identity (`bronzeward token issue`) |
| `forbidden` | 403 | The token lacks the role, or a human-only act from automation | Check `auth.roles` and the groups claim |
| `machine-identity-mismatch` | 409 | The node's SMBIOS UUID, node ID or cluster ID is not the recorded one | Check the endpoint points at the right machine |
| `talos-access-unavailable` | 503 | No talosconfig in the provider, access denied, or the node unreachable | Check OpenBao, the access path and the network |
| `dependency-unavailable` | 503 | PostgreSQL, the identity provider or OpenBao is unreachable or not configured | Fix the dependency, then retry |
| `transient-conflict` | 503 | Concurrent writes kept colliding | Retry with the same key |
| `epoch-superseded` | 503 | This process started before the current recovery epoch | Restart it |
| `internal-error` | 500 | Unknown; the write may or may not have happened | Retry with the same key, which returns the first answer if it was recorded |

## Reading the timeline

Each machine has one append-only timeline: every plan, approval, observation, commitment, attempt,
response, accounting, drift, freeze and ownership transition, in revision order.

```sh
call GET "/machines/$machine_w/timeline?limit=500" "$viewer" | jq -c '.items[] | [.revision, .at, .kind, .entry]'
```

Pages hold 50 entries by default and at most 500; follow `next` with `?cursor=`. Responses never
carry configuration text or secrets.

From it, for any operation, you can answer:

- **Who approved what:** the `plan` and `approval` entries, and any `approval-revocation`.
- **What each attempt carried, and when:** `attempt` entries, each with its number, route, controller
  and transport and verification deadlines.
- **Whether it was sent:** a `response`, an `accounting`, or neither (still unknown).
- **What was seen on the node:** `observation-started` and `observation` entries, each naming its
  purpose.
- **Why the scope was held:** `commitment`, `operation-state` and `scope-state` entries.
- **Who decided an accounting, and on what:** a decided `accounting` entry names who decided it, the
  start and end observations and the facts.
- **How it ended:** the last `operation-state` entry.
- **Who owned it:** `ownership-transition` entries, each with its generation.

To follow one operation:

```sh
call GET /operations/$op "$viewer" | jq .
call GET /operations/$op/events "$viewer" | jq -c '.items[]'
curl --disable -sS -N -H @- -H 'Accept: text/event-stream' "$A/api/v1/operations/$op/events" \
  <<<"Authorization: Bearer $viewer"
```

The stream ends when the operation ends, or when the token expires. Resume with a fresh token and
`Last-Event-ID` set to the last event's `id`. An `event: reset` means the installation entered a
new recovery epoch: read the events again from the start.

## Common situations

- **An operation stays `unresolved`.** Its owner stopped. See
  [interrupted operations](recovery.md#interrupted-operations).
- **A plan is approved but nothing happens.** Look on the timeline for a `refusal` entry, which names
  the transaction and the comparison that failed, or a `use-time-check` entry, which gives each
  dependency's result.
- **A machine is frozen.** Its last `freeze` entry says who froze it, in which role and why. An
  approver unfreezes it with `POST /machines/$machine/unfreezes` and a body `{"reason": "..."}`.
- **Every call answers `409 recovery-mode-active`.** The instance was started with
  `-recovery-start` and no entry is recorded. Until [the entry](recovery.md#3-record-the-entry)
  commits it serves only `/livez` and `POST /recovery/entries`, `GET /recovery` included; the
  `detail` says so.
- **Acts on one machine answer `409 recovery-mode-active`.** The installation is in recovery mode
  and that machine's scope is not released. On any instance past the entry:
  `call GET /recovery "$viewer" | jq '{recoveryMode, scopes}'`.
- **`serve` exits at start.** An unmigrated or differently migrated database: stop every server,
  run `migrate` ([migrate, then serve](README.md#migrate-then-serve)).
