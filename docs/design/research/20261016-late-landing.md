# Late landing through the control plane's API proxy: a measured distribution

| | |
|---|---|
| **Date** | 16 October 2026 |
| **Work item** | [Recovery and failure behaviour](https://github.com/ginsys/bronzeward/issues/28) |
| **Design reference** | [§12.5 Durable operations and uncertain outcomes](../Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes); specification [execution-recovery §5.2](../../spec/execution-recovery.md#52-accounting-a-lost-response), [§9.3](../../spec/execution-recovery.md#93-gaps-carried-and-what-would-close-them) item 2, [§10](../../spec/execution-recovery.md#10-choices-for-owner-review) choices 9 and 11; [acceptance plan S6.2](../../spec/acceptance-plan.md#s62-late-landing-bound-measurement) |
| **Artifacts** | [`experiments/e4-dispatch-safety/run/measure`](../../../experiments/e4-dispatch-safety/run/measure); evidence under [`evidence-latelanding/`](../../../experiments/e4-dispatch-safety/evidence-latelanding/) (the full run) and [`evidence-latelandingsockets/`](../../../experiments/e4-dispatch-safety/evidence-latelandingsockets/) (a subset with socket samples) |
| **Decision enabled** | Whether a bound on when an abandoned request can no longer land exists for the control plane's proxied route, which a contract amendment could adopt for accounting (ER §5.2). It re-admits no route and changes no contract. |

## 1. Question

The [dispatch-safety experiment](20260925-dispatch-safety.md#44-uncertain-sends-reconnects-and-accounting-criteria-2-and-3)
(DS) saw one request sent through the control plane's API proxy land about 11 s after a partition
healed, after the client that sent it had been killed at its transport deadline. That happened once
in four proxied partitions, and DS could not explain why. ER §5.2 therefore leaves the accounting of a
lost response to a `recovery-admin` decision after a 30 s settle floor (choice §10.11), and choice
§10.9 keeps the proxied route out of the PoC's dispatch until this measurement.

S6.2 asks for three things:

1. from the Talos v1.13.6 source, how the proxy forwards and cancels a request, and what that
   predicts;
2. landings through the proxy across partition lengths, pauses of each side and connection reuse,
   enough per cell to state a distribution;
3. over the worker's own endpoint, whether a dial failure proves that nothing was sent.

## 2. What the source predicts

This section is the prediction as written before the measurement ran. Three cells departed from it;
§5.3 gives what the source shows about why.

Talos v1.13.6 pins `github.com/siderolabs/grpc-proxy` v0.5.1 and `google.golang.org/grpc` v1.81.0
(`go.mod:157`, `:191`), and its nodes report Go 1.26.5. Paths below are in those trees.

**The proxy keeps one connection per worker and ties the forwarded call to the client's.**
apid's backend caches one `grpc.ClientConn` per target and reuses it for every request
(`internal/app/apid/pkg/backend/apid.go:70-127`). The connection has no keepalive, a 15 min idle
timeout (`:112`, half of `GracefulShutdownTimeout` at `:36`) and a 20 s `MinConnectTimeout` (`:117`).
The proxy derives the forwarded stream's context from the client's (`proxy/handler.go:58-73`), so
when `talosctl` goes away, apid cancels the forwarded stream, and grpc-go queues an `RST_STREAM`
behind the request's frames on that same connection.

**During a partition the frames wait in the control plane's TCP connection.** Nothing in apid or
grpc-go closes the connection on a stalled write. The request's HEADERS and DATA, then the
`RST_STREAM`, stay queued and are retransmitted on TCP's backoff. ASSUMPTION, not read from the
kernel source: Linux defaults (minimum retransmission timeout 200 ms, doubling; `tcp_retries2` 15)
keep retrying for far longer than any partition here, so after the heal the next retransmission
delivers all three, in order.

**On the worker, the request then races its own cancellation.** grpc-go's reader handles the
frames in order. HEADERS starts the handler; DATA is queued for it; `RST_STREAM` closes the stream
and cancels its context (`internal/transport/http2_server.go:820-833`, `:1344-1362`). The handler
reads its request through `recvBufferReader` (`internal/transport/transport.go:136-193`), and each
read is a `select` between the cancelled context and the queued data. When both are ready Go picks
either case at random, so arrival does not decide landing. Only once the whole message is read does
`processUnaryRPC` call the handler (`server.go:1386`).

**Once machined's handler runs, it lands.** `ApplyConfiguration`
(`internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go:178-315`) takes its lock with
`TryLock` (`:183`) and uses the context only in `RuntimeValidate` (`:229`), which uses it only to
match an install disk (`pkg/machinery/config/types/v1alpha1/v1alpha1_validation.go:987-992`). The
no-reboot path (`:308-315`) never checks it.

What this predicts, per cell:

| Fault | Prediction |
|---|---|
| netsplit of the worker, warm connection | frames delivered by the first retransmission after the heal; lands in some captures, not all (the race above); landing seconds after the heal, growing with the partition as the backoff doubles |
| pause of the worker | the kernel keeps acknowledging while the container's processes are frozen (ASSUMPTION), so the frames wait in the worker's socket buffer and race the same way after the unpause |
| pause of the control plane | apid is frozen with the request unread; after the unpause it reads a request whose client is gone; whether it forwards before it sees that is the same kind of race, one hop earlier |
| cold connection (control plane restarted first) | apid must dial the worker under the partition; `talosctl` is killed at 15 s, before the 20 s `MinConnectTimeout`, so the dial is cancelled and nothing is sent: no landing |
| the worker's own endpoint | the TCP handshake cannot complete, so nothing is sent: no landing |

apid cannot be restarted through the Talos API: a service must opt in
(`internal/app/machined/pkg/system/system.go:469-477`), and in `pkg/system/services/` only
`kubelet.go` and `extension.go` do. The cold cell therefore kills and starts the control-plane
container (`fixtures/bin/inject kill|start controlplane`).

## 3. What was built

`run/measure` does not use `e4x` or the database. Each capture:

1. writes a fresh artifact, the worker's configuration with a node label unique to the capture;
2. reads the worker's resource version straight from the worker; on a warm route, reads `version`
   through the control plane so the proxy holds a live connection; on the cold route, kills and
   starts the control plane first;
3. records the time, injects the fault, then runs `talosctl apply-config --mode=no-reboot` with
   `timeout --signal=KILL 15`, as `e4x` does at its transport deadline;
4. sleeps until the cell's fault length, undoes the fault, then reads the worker's resource version
   once a second for 60 s, recording the first change;
5. records machined's `ApplyConfiguration` log lines since the capture's start time, and one line
   in `captures.tsv`.

Before the next capture it checks whether the previous capture's version moved after its window
closed (`after-window.tsv`). The control cell, with no fault, must land, or a run without landings
proves nothing. A second control, `control-after`, runs last, so that a run of no landings is not a
path the faults left broken; it was added after the full run (§5.1), and the socket run (§5.2) has
it.

With `E4M_SOCKETS=1`, each capture also lists, once a second from both nodes' network namespaces,
the TCP connection between the control plane and the worker's API port (`/proc/net/tcp` and
`tcp6`: state, send and receive queues, the timer, the retransmission count). `E4M_ONLY` selects
cells; a subset is collected only into a directory of its own.

## 4. Reproduction

As the [experiment README](../../../experiments/e4-dispatch-safety/README.md#the-late-landing-measurement)
gives it: a fresh `fixtures/bin/up`, then `E4D_OUT=<new dir> run/measure`, the fixture's evidence
bundle, `run/collect-evidence`, `fixtures/bin/down`. The socket subset is the same on a second
fresh fixture, with `E4M_N=3 E4M_SOCKETS=1` and the `E4M_ONLY` expression the README gives, collected
with `E4D_EVIDENCE=<checkout>/experiments/e4-dispatch-safety/evidence-latelandingsockets`. Each
`run.txt` records the commit, the `talosctl` versions, the capture count, the deadline, the window
and any cell selection.

## 5. Results

### 5.1 The full run

Twelve cells of five captures each, 21:04-22:47 UTC on 15 October, at the branch commit `run.txt`
names (before `control-after` was added). Times are from `captures.tsv`; "after the send" is from
`sent`, the moment `talosctl` started.

| Cell | Landed | `talosctl` | When it landed |
|---|---|---|---|
| control, no fault | 5/5 | exit 0 after about 25 ms | seen by the first read, about 60 ms after the send |
| netsplit of the worker, 20 s | 5/5 | killed at 15 s | 26-28 s after the send, 6-7 s after the heal |
| netsplit of the worker, 45 s | 0/5 | killed at 15 s | — |
| netsplit of the worker, 90 s | 0/5 | killed at 15 s | — |
| pause of the worker, 20, 45, 90 s | 0/15 | killed at 15 s | — |
| pause of the control plane, 20, 45, 90 s | 0/15 | killed at 15 s | — |
| cold connection, netsplit 20 s | 0/5 | 4 failed after 3 s (`connect: no route to host`), 1 killed at 15 s | — |
| the worker's own endpoint, netsplit 20 s | 0/5 | killed at 15 s | — |

In every capture machined logged exactly one `ApplyConfiguration` when the version moved and none
when it did not. No version moved after a capture's window closed: there is no `after-window.tsv`.
Each landing is the capture's own: every capture's starting configuration (`base.yaml`, kept with
the run's outputs because it holds the fixture's secrets) carries the node label of the last capture
recorded as landed before it, in all 60 captures here and all 15 of the socket run.

The 20 s landings are tight. The worker's `updated` time (whole seconds) is 26.4-27.1 s after the
send in all five, and the harness first saw the new version 27.5-27.6 s after it. The heal came at
20.3 s.

### 5.2 The socket run

Five cells of three captures each, 23:15-23:37 UTC on 15 October, on a second fresh fixture, with
`E4M_SOCKETS=1`. The landings repeat the full run, with one exception: one 20 s netsplit did not
land.

| Cell | Landed | What the samples show |
|---|---|---|
| control, first and last | 6/6 | an idle connection on both ends |
| netsplit of the worker, 20 s | 2/3, 26-27 s after the send | the control plane's send queue holds about 3 KB through the split, 35 bytes more from about 15 s, when `talosctl` was killed; it empties at the seventh retransmission, about 26 s after the send, and the connection survives on both ends in all three captures, the one that did not land included |
| netsplit of the worker, 45 s | 0/3 | the worker's end disappears 29-30 s after the split began; the control plane's end disappears at its eighth retransmission, 53-54 s after the send and 8-10 s after the heal |
| pause of the worker, 20 s | 0/3 | nothing waits to be sent; the frozen worker's receive queue holds about 3 KB from the pause onward, and the same 35 bytes more from about 15 s |

The capture that did not land shows that the request was delivered and the connection lived, so
the request was lost on the worker, not in transport: the race §2 predicted.

### 5.3 What the source shows about the departures

Four results departed from §2: no netsplit of 45 or 90 s landed, no pause of the worker landed,
a pause of the control plane never let the request leave `talosctl`, and the cold dial failed fast
instead of being cancelled. What follows was read after the full run, then checked
against the socket run (§5.2).

**The worker's apid is a second proxy.** A request for a node arrives at that node's apid, which
forwards it to machined over a local socket (`internal/app/apid/pkg/director/director.go:62-63`, the
`proxyfrom` metadata the control plane adds; `internal/app/apid/service.go:79`). The race in §2
therefore runs at the worker's apid before machined.

**The worker's end of the connection gives up after about 30 s of silence.** grpc-go's server sets
its keepalive time to 2 h and its timeout to 20 s when none is configured
(`internal/transport/defaults.go:38-39`, `internal/transport/http2_server.go:230-235`), and then sets
`TCP_USER_TIMEOUT` to that 20 s on every accepted connection (`:236-240`). Go enables keepalive on
every accepted connection with a 15 s idle time and a 15 s interval (`net/tcpsock_posix.go:163`,
`net/tcpsock.go:289-293`, `net/dial.go:20-27`), and apid configures neither. ASSUMPTION, not read
from the kernel source but matched by the samples: Linux aborts an idle socket at the first
keepalive probe at which the user timeout has passed since the last receipt, so the worker's socket
goes about 30 s after it last heard from the control plane.

**The control plane's end retransmits on its own schedule.** It holds the unacknowledged request,
so its keepalive does not run and nothing bounds it short of `tcp_retries2`. ASSUMPTION, as above:
from Linux's 200 ms minimum retransmission timeout, doubling, the retransmissions fall about 0.2,
0.6, 1.4, 3.0, 6.2, 12.6, 25.4, 51.0 and 102.2 s after the first send; the samples show the same
schedule, slightly later (the seventh at about 26 s, the eighth at 53-54 s). A 20 s heal is
answered by the seventh, which arrives before the worker's 30 s abort: the 20 s landings at
26-28 s. After a 45 s or 90 s heal, the next retransmission reaches a worker that has no socket any
more; ASSUMPTION: it answers with a reset, and the control plane closes its end. The socket run's
45 s captures show the outcome: the worker's end disappears 29-30 s after the split, and the control
plane's at the first retransmission after the heal, its send queue still holding the request and
the 35 bytes the cancellation added.

**A pause of the worker keeps the connection alive, and the cancellation arrives first.** A frozen
container's kernel still acknowledges, so in a pause neither end times out and the request is
delivered into the frozen process's receive buffer; the socket run's paused captures show both,
with nothing left to send on the control plane. Every pause here outlasts the 15 s deadline, so by
the time apid resumes, the control plane's `RST_STREAM` is in the buffer behind the request
(ASSUMPTION: the 35 bytes in §5.2 are that frame in its TLS record). Hypothesis, not proven by the
source: the resumed process reads both at once and cancels the stream before it forwards
anything. In a netsplit both also wait in the control plane's send queue, but after the heal the
retransmission restarts from one segment (ASSUMPTION: Linux's congestion window after a timeout),
so the request can reach the worker's apid ahead of its cancellation. That would be why most 20 s
captures land and one did not.

**A pause of the control plane stops the request in `talosctl`.** The harness pauses the control
plane before it starts `talosctl`, so `talosctl` opens a fresh connection to a frozen apid. The
frozen apid cannot answer the TLS handshake, and grpc-go sends a call only on a ready transport
(`picker_wrapper.go:97-105`; ASSUMPTION for the kernel's part: the TCP handshake itself completes in the frozen
container's listen queue). The request therefore never leaves `talosctl`, and when `talosctl` is
killed there is nothing in flight. §2's prediction for this cell assumed a request already with
apid; this measured a request that never reached it.

**The cold dial failed on address resolution.** The restarted control plane had no address for the
worker; ASSUMPTION: Linux gives up resolving after about 3 s and the dial fails with
`EHOSTUNREACH`, as four captures show. In the fifth, the dial was still waiting when `talosctl` was
killed. Killing `talosctl` cancels the forwarded call, not the proxy's cached connection, whose dial
has its own lifetime (`clientconn.go:918` in grpc-go). The call cannot be sent before a connection
exists, and once cancelled it no longer waits for one, so whichever way the dial ended, nothing was
sent for it. The control plane's apid logs the call as a success, because in its one-to-many mode
an error from one node is carried inside the response.

## 6. What this decides

S6.2's pass criterion is a recorded distribution with its method and prediction, whichever way it
falls. This is it. What it supports:

- **After a netsplit, the latest landing seen was 28 s after the send.** Requests abandoned during
  a 20 s partition of the worker landed in 7 of 8 captures, 26-28 s after the send; after a 45 s or
  90 s partition none of 13 did, and in every 45 s socket sample the worker's end of the connection
  was gone about 30 s after the split began. The explanation in §5.3 rests on grpc-go's server
  default and Go's keepalive default, read from source, and on Linux's keepalive and retransmission
  behaviour, assumed. If it holds, delivery stops once the worker's socket is gone, but that bounds
  delivery, not completion: a request delivered before then still runs to the end. Talos configures
  none of these defaults, a version change to any of them can move the cut-off, and a connection
  idle before the send would reach it sooner.
- **A pause of the worker has no such cut-off.** The connection stays up for as long as the process
  is frozen, and the request waits in a buffer. No paused worker capture landed (0 of 15 in the
  full run, 0 of 3 in the socket run), but the reason is a hypothesis about the order in which
  buffered frames are read, not a guarantee in the source. A pause of the control plane before the
  send kept the request in `talosctl` (0 of 15).
- **A dial that fails sends nothing.** Over the worker's own endpoint and on the cold route, the
  request was never sent and nothing landed (0 of 10). This answers S6.2 step 3 for a dial that
  fails before the connection exists; it says nothing about a connection that fails after it
  opened.

So this measurement does not supply a bound that [ER §5.2](../../spec/execution-recovery.md#52-accounting-a-lost-response)
can adopt. Nothing changes in the contract: accounting a lost response stays a `recovery-admin`
decision after the settle floor, a late landing is detected as drift, not prevented, and the
proxied route stays out of the PoC's dispatch (choice §10.9). The settle floor of 30 s (choice
§10.11) happens to sit at the socket abort this fixture showed; that agreement is not a reason to
rely on it. An amendment that re-admits the route would have to configure the keepalive and user
timeout it depends on, on both ends, establish the kernel behaviour assumed here, and close the
pause case, which no default here bounds.

## 7. Limits

- **The window is 60 s.** A landing later than that shows only in `after-window.tsv`, with the
  next capture's start as its upper bound on time.
- **Whole-second times.** The worker's `updated` field has whole-second resolution; the first-seen
  time comes from once-a-second reads.
- **One host, one network.** The fixture's nodes are containers on one Docker bridge. A
  `netsplit` is `docker network disconnect`; a real partition, a lossy link or a routed network
  can retransmit, reorder or reset differently.
- **The kernel's retransmission and keepalive behaviour is assumed**, not read from source or
  configured: the delays in §5 are what this host's defaults produced, and §5.3 marks each step
  that rests on them.
- **Five captures per cell** state a distribution, not a probability small enough to rely on.
  The socket run adds three per cell it covers.
- **Partitions and pauses of 20, 45 and 90 s only.** Between 20 and 45 s the transport schedule
  in §5.3 predicts the change, but no capture tested where it falls. Faults shorter than the 15 s
  deadline were not measured; under the same schedule, a partition healed at 14 s would still wait
  for the retransmission at about 26 s, after `talosctl` was killed.
- **Pauses of the control plane began before the send**, so they measured a request that never
  left `talosctl`, not a pause with a request already at the proxy.
- **Every warm capture sent on a connection the harness had just used.** A connection idle for
  longer before the send reaches the worker's abort sooner (§5.3); none was measured.
- **The full run predates the closing control.** Its last cells (cold, direct) are not followed
  by a control; the socket run's `control-after` landed (§5.2).
- **`run.txt` names the branch commit the run was made from**, which a rebase merge does not keep.
  After both runs, `run/measure` was corrected on paths neither run reached: an undo armed before
  each fault rather than after it, and a failed last read now recorded as `unknown`. In both runs
  every capture's `landed` agrees with its version change and no read failed.
