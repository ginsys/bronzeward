# A terminal operation read with no epoch header, then pages of another epoch

Date: 7 November 2026. Issue: ginsys/bronzeward#695. Status: investigation draft. The client test ran twice with `-race`, and the finding reproduces on the second run. The two command tests print the stale success. A third run with `-race` ran all three tests together, and all three failed (section 3). The tests land with a `t.Skip` on their first line, so the shas in section 3 are those of the files as they ran, without the skip. One later change touches `headerless_watch_test.go`: after the pages, the fake stream also answers in the new epoch, as the rest of the fake does. A run without `-race`, with the skips removed through a build overlay (stamp w695-c2), printed the same two failures.

Pinned version: main at commit 877013a2. The reading and the run use that commit with no change to tracked files. The reading began at commit 0b3a42da. No file under `internal/client` or `cmd/bronzewardctl` differs between the two commits. The same holds for `internal/api/api.go`, `docs/spec/client-contracts.md`, `go.mod` and `go.sum`. So the line numbers below hold at 877013a2.

## 1. Question

`Client.Watch` follows an operation to its end. After a terminal read it drains the event pages. If those pages show another epoch, the installation was restored since the read, and the read no longer stands (`docs/spec/client-contracts.md` section 8.6 item 2). Does `WatchBody` still return that read when the read had no `Bronzeward-Epoch` header? Do `publish` and `import` then print a stale state?

Out of scope: a fix. Any remediation is a separate issue.

## 2. Reading of the code

- `Watch` drops the `OperationRead` that `WatchBody` returns (`watch.go:75-78`).
- `EpochHolds` compares the count of epoch moves, not the epoch, so it also judges a headerless read (`watch.go:89-96`). The count rises in `observe` when an answer shows another epoch (`client.go:334-341`).
- After a terminal read the watch drains the pages (`watch.go:177`). The clean branch is `epoch == "" || held && (...)` (`watch.go:201-203`). With `epoch == ""` it returns the read at once. The value `held` is not read, although it is false when the pages show another epoch.
- `publish` and `import` call `Watch` and use `o.State` with no count check (`cmd/bronzewardctl/publish.go:67`, `cmd/bronzewardctl/imports.go:387`). `operation watch` calls `WatchBody` and checks `EpochHolds` (`cmd/bronzewardctl/opwatch.go:358`).
- A real server sets the header on every authenticated success (`internal/api/api.go:331-332`). An authenticated request can still get a 503 with no header, when the database read at `internal/api/api.go:279-283` fails. That answer is an error, not a terminal operation read. The shape needs a server or proxy that breaks section 8.5. This is the premise for the severity of the finding. It does not dismiss the finding, because section 8.6 item 2 tells the client to judge such a read by the count.

## 3. Reproduction

Test: `TestWatchBodyHeaderlessTerminalRestored` in `internal/client/watch_headerless_test.go`. It uses the fake server of `write_test.go`.

Input, all synthetic:

1. The stream answers with no event.
2. The first two operation reads answer `succeeded` with no epoch header. The test makes the first one itself and asserts that the client sees no header and no move. The watch makes the second.
3. The event pages answer epoch `ep_BWSYNTH2` with one event `ep_BWSYNTH2:1`. The client began in `ep_BWSYNTH1`. The test asserts that the pages moved the count once, through `ListAt`.
4. A third operation read, if the watch makes one, answers `succeeded` in `ep_BWSYNTH2`.

Command: `go test -race -run TestWatchBodyHeaderlessTerminalRestored -v ./internal/client`

Expected by the specification: `WatchBody` returns an error, or a read for which `EpochHolds` is true.

Run 1 (stamp w695-l1) did not reach the assertion. The test stopped at its second premise: the count of moves was 2, not 1. The cause was in the test. After the pages of `ep_BWSYNTH2`, the client reads `GET /server` again (`client.go:167`, section 8.5). The fake server answered that read in `ep_BWSYNTH1`, so the client saw e2 and then e1, and counted a second move. The fix is in the test only: when the pages answer, the fake switches its default epoch to e2, as a restored server would. No client code was involved, and the expected count stays 1.

Run 2 (stamp w695-r1, `-race`, same commit) reached the assertion and failed it. Both premises held. The line is `watch_headerless_test.go:52`: `WatchBody returned the "succeeded" read made before the restore (read at move 0, count now 1) with no error; want an error or a read that stands`. So `WatchBody` returns the headerless terminal read although the pages showed another epoch. `EpochHolds` is false for that read, because its count is 0 and the client's count is 1. The race detector printed no report. The run has no positive control for the detector, so this does not show the code is free of races. In the same run, `cmd/bronzewardctl` passed. The test file has the sha256 e13dcd9b0995ec56107b57ab6995f00b7064fbc5de4d4567b379d2ded46ab318.

Run 3 (stamp w695-r2, `-race`, same commit) ran all three tests together, unskipped. Exactly three tests failed, and nothing else: the client test at `watch_headerless_test.go:52` with the same message as run 2, and the two command tests below. Both packages built, and no other test failed. The race detector printed no report, and the same limit holds as for run 2. The commands in the next part printed the same output in this run as without `-race`, except for the port numbers.

### Observed exit and output of the two commands

Tests: `TestPublishHeaderlessTerminalRestored` and `TestImportWatchHeaderlessTerminalRestored` in `cmd/bronzewardctl/headerless_watch_test.go`. The file has the sha256 d601a42470d9bb3cb86d44623653cbb1d16b3f8deccae3945459b3ba68620bad.

Input, all synthetic, the same shape for both commands. The event stream ends with no event. The first read of the operation answers `succeeded` with no epoch header. The event pages answer epoch `ep_BWSYNTH-2` with one `failed` event. From the pages on, every answer carries `ep_BWSYNTH-2`, `GET /server` included, and the operation reads `failed`. So the restored installation shows the operation failed. The first read does not stand (section 8.6 item 2).

Command: `go test -count=1 -run 'TestPublishHeaderlessTerminalRestored|TestImportWatchHeaderlessTerminalRestored' -v ./cmd/bronzewardctl`. This run is at commit 877013a2 without `-race` (stamp w695-c1). Each test asserts a non-zero exit and no success text on standard output. Both tests failed at that assertion, and the output below is verbatim from the log.

`publish`: exit 0.

```
stdout:
Publishing draft drf_BWSYNTH revision 1 (operation op_BWSYNTH-pub).
Publication succeeded: release rel_BWSYNTH is now Desired for 2 machines. Nothing has been sent to any machine.
  mch_a has Applied release rel_BWSYNTH-old: plan an apply-config:
    bronzewardctl plan create --release 'rel_BWSYNTH' --machine 'mch_a' --operation 'apply-config'
  mch_b has no Applied release: hand it over with an adopt plan:
    bronzewardctl plan create --release 'rel_BWSYNTH' --machine 'mch_b' --operation 'adopt'
Review a machine's rendered configuration: bronzewardctl release review rel_BWSYNTH <machine>

stderr:
lab (http://127.0.0.1:41065): GET /api/v1/operations/op_BWSYNTH-pub answered without Bronzeward-Epoch, a server defect
lab (http://127.0.0.1:41065): The installation entered recovery mode or was restored; what was read before may no longer hold.
  failed
```

`import continue`: exit 0.

```
stdout:
Continued import ing_BWSYNTH (operation op_BWSYNTH-ing).
Import ing_BWSYNTH released into draft drf_BWSYNTH (revision 2), import base ibr_BWSYNTH.
This client did not start import ing_BWSYNTH, so it keeps no ETag of draft drf_BWSYNTH. Read the draft before writing to it:
  bronzewardctl --context 'lab' draft show 'drf_BWSYNTH'

stderr:
lab (http://127.0.0.1:41905): POST /api/v1/ingestions/ing_BWSYNTH/continuations is for a person signed in, not an automation token: the server refuses it.
lab (http://127.0.0.1:41905): GET /api/v1/operations/op_BWSYNTH-ing answered without Bronzeward-Epoch, a server defect
lab (http://127.0.0.1:41905): The installation entered recovery mode or was restored; what was read before may no longer hold.
  failed
```

Reading of the result. Both commands print the success of the read made before the restore and exit 0. Standard error shows the notice about the restore and the `failed` event of the new epoch, so the client knew. The words "Publication succeeded" and "released into draft" come from the stale read.

Two limits of this run. The first `POST` line in the import output is a client note about the automation token of the test context. The fake server accepted the request, and the line is not part of the finding. The run has no `-race`, and the commands run against fakes, not against a real server.

## 4. Alternatives

1. Test `held` at `watch.go:202` when `epoch == ""`: read again, as for a read with a header.
2. Return the `OperationRead` from `Watch`, and make `publish` and `import` check `EpochHolds`, as `operation watch` does.
3. Do both. The first keeps the watch correct for every caller. The second keeps a caller from relying on it.

Not chosen here: treating a headerless terminal read as an error. Section 8.5 calls the missing header a server defect, but the spec judges such a read by the count, not by refusal.

## 5. Decision enabled

Whether to file a remediation issue, and for which of the alternatives. The test stays as the evidence if the observed result is red.

Outcome: ginsys/bronzeward#728 took alternative 3. The headerless clean branch of `WatchBody` now tests the count of epoch moves, and `Watch` returns the `OperationRead`, which `publish` and `import` check with `EpochHolds`. The three tests run with no skip.

## 6. Limits

- Severity premise: the shape needs a non-conforming server or proxy. No such server is known. The specification still defines the client's reading for it.
- The two commands run against fake servers in `cmd/bronzewardctl`, as section 3 shows. No real server was involved.
