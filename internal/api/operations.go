package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// attemptBody is one attempt of an apply-config operation (execution-recovery.md §3.3, §5) with
// the response and the accounting recorded for it, null until recorded. The response's text is not
// served: it is the node's own and has no redacted form (compilation §8.3).
type attemptBody struct {
	ID                   string             `json:"id"`
	Number               int                `json:"number"`
	Controller           string             `json:"controller"`
	Generation           int64              `json:"generation"`
	Route                string             `json:"route"`
	Observation          string             `json:"observation"`
	TransportDeadline    time.Time          `json:"transportDeadline"`
	VerificationDeadline time.Time          `json:"verificationDeadline"`
	At                   time.Time          `json:"at"`
	Revision             int64              `json:"revision"`
	Classification       *int64             `json:"classification"`
	Response             *attemptResponse   `json:"response"`
	Accounting           *attemptAccounting `json:"accounting"`
}

type attemptResponse struct {
	Class    string    `json:"class"`
	Code     int       `json:"code"`
	At       time.Time `json:"at"`
	Revision int64     `json:"revision"`
}

type attemptAccounting struct {
	Basis    string    `json:"basis"`
	At       time.Time `json:"at"`
	Revision int64     `json:"revision"`
}

// readAttempts reads operation v's attempts by number.
func readAttempts(ctx context.Context, tx *sql.Tx, v string) ([]attemptBody, error) {
	rows, err := tx.QueryContext(ctx, `SELECT a.id, a.number, a.owner, a.owner_gen, a.route::text, a.observation, a.transport_deadline,
			a.verification_deadline, a.at, a.revision, a.classification, r.class, r.code, r.at, r.revision, c.basis, c.at, c.revision
		FROM attempt a
		LEFT JOIN attempt_response r ON r.attempt = a.id
		LEFT JOIN attempt_accounting c ON c.attempt = a.id
		WHERE a.operation = $1 ORDER BY a.number`, v)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []attemptBody
	for rows.Next() {
		var b attemptBody
		var class, basis sql.NullString
		var code, respRev, accRev, cls sql.NullInt64
		var respAt, accAt sql.NullTime
		if err := rows.Scan(&b.ID, &b.Number, &b.Controller, &b.Generation, &b.Route, &b.Observation, &b.TransportDeadline,
			&b.VerificationDeadline, &b.At, &b.Revision, &cls, &class, &code, &respAt, &respRev, &basis, &accAt, &accRev); err != nil {
			return nil, err
		}
		b.TransportDeadline, b.VerificationDeadline, b.At = b.TransportDeadline.UTC(), b.VerificationDeadline.UTC(), b.At.UTC()
		if cls.Valid {
			b.Classification = &cls.Int64
		}
		if class.Valid {
			b.Response = &attemptResponse{Class: class.String, Code: int(code.Int64), At: respAt.Time.UTC(), Revision: respRev.Int64}
		}
		if basis.Valid {
			b.Accounting = &attemptAccounting{Basis: basis.String, At: accAt.Time.UTC(), Revision: accRev.Int64}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

var (
	operationKinds  = []string{"publish", "ingest", "apply-config", "adopt"}
	operationStates = []string{"queued", "running", "succeeded", "failed", "committed", "sending", "verifying", "completed",
		"rejected", "cancelled", "unresolved"}
)

// listOperations is GET /operations (§9.2), in identifier order, filtered by the plan, the machine,
// the state and the kind. A plan's operation exists from its commitment (§8.1).
func listOperations(a *API, w http.ResponseWriter, q *request) {
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		limit, after, ref := page(q, id.Operation, "plan", "machine", "state", "kind")
		if ref != nil {
			return "", nil, ref
		}
		v := q.r.URL.Query()
		plan, machine, state, kind := v.Get("plan"), v.Get("machine"), v.Get("state"), v.Get("kind")
		if (plan != "" && id.MustHave(plan, id.Plan) != nil) || (machine != "" && id.MustHave(machine, id.Machine) != nil) ||
			(state != "" && !slices.Contains(operationStates, state)) || (kind != "" && !slices.Contains(operationKinds, kind)) {
			return "", nil, refuse(http.StatusBadRequest, "invalid-request", "plan, machine, state and kind each name a value of their kind")
		}
		out, err := pageRows(ctx, tx, q, limit, after, selectOperation+` WHERE id > $1 AND ($3 = '' OR plan = $3)
			AND ($4 = '' OR machine = $4) AND ($5 = '' OR state = $5) AND ($6 = '' OR kind = $6) ORDER BY id LIMIT $2`,
			func(r *sql.Rows) (operationBody, string, error) {
				b, err := scanOperation(r)
				return b, b.ID, err
			}, plan, machine, state, kind)
		return "", out, err
	})
}

// eventBody is one event of an operation (§8.3). Its identifier is `<epoch>:<number>` in the epoch
// read, so a client resumes with it only within that epoch; type is the entry's kind for a plan
// operation and the event's own type for a publish or an ingest, and data is the entry itself.
type eventBody struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

// opHead is what an event read needs of its operation.
type opHead struct{ kind, state, machine string }

func readHead(ctx context.Context, tx *sql.Tx, v string) (opHead, error) {
	var h opHead
	var machine sql.NullString
	if id.MustHave(v, id.Operation) != nil {
		return h, refuse(http.StatusNotFound, "not-found", "no such resource")
	}
	err := tx.QueryRowContext(ctx, `SELECT kind, state, machine FROM operation WHERE id = $1`, v).Scan(&h.kind, &h.state, &machine)
	if errors.Is(err, sql.ErrNoRows) {
		return h, refuse(http.StatusNotFound, "not-found", "no such resource")
	}
	h.machine = machine.String
	return h, err
}

// terminal reports that no entry will follow the state: unresolved is not terminal, a recovery
// administrator resolves it (execution-recovery.md §5.2).
func (h opHead) terminal() bool {
	switch h.state {
	case "succeeded", "failed", "completed", "rejected", "cancelled":
		return true
	}
	return false
}

// eventQuery selects the events after the number $1, at most $2 of them. A plan operation's are the
// machine entries that name it, but for its use-time check (which names the plan, before the
// commitment) and the starts of its observations (the timeline holds them). No entry is served with
// the node's response text or an artifact digest.
func (h opHead) eventQuery(op string) (string, []any) {
	if h.machine == "" {
		return `SELECT number, at, COALESCE(entry->>'type', kind), entry::text FROM operation_event
			WHERE operation = $3 AND number > $1::text::bigint ORDER BY number LIMIT $2`, []any{op}
	}
	return `SELECT revision, at, kind, (entry - 'text' - 'digest')::text FROM machine_event
		WHERE machine = $3 AND entry->>'operation' = $4 AND kind <> 'observation-started' AND revision > $1::text::bigint
		ORDER BY revision LIMIT $2`, []any{h.machine, op}
}

// scanEvent returns the scan of one eventQuery row in epoch, and its number.
func scanEvent(epoch string) func(*sql.Rows) (eventBody, string, error) {
	return func(r *sql.Rows) (eventBody, string, error) {
		var e eventBody
		var n int64
		var data string
		if err := r.Scan(&n, &e.At, &e.Type, &data); err != nil {
			return e, "", err
		}
		num := strconv.FormatInt(n, 10)
		e.ID, e.At, e.Data = epoch+":"+num, e.At.UTC(), json.RawMessage(data)
		return e, num, nil
	}
}

// getOperationEvents is GET /operations/{id}/events: a cursor-paginated JSON list, or a stream
// when the request accepts text/event-stream (§8.3).
func getOperationEvents(a *API, w http.ResponseWriter, q *request) {
	if strings.Contains(q.r.Header.Get("Accept"), "text/event-stream") {
		if q.r.URL.RawQuery != "" {
			a.fail(w, q, refuse(http.StatusBadRequest, "invalid-request", "an event stream takes no query; it resumes with Last-Event-ID"))
			return
		}
		streamEvents(a, w, q)
		return
	}
	op := q.r.PathValue("id")
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		h, err := readHead(ctx, tx, op)
		if err != nil {
			return "", nil, err
		}
		limit, after, ref := pageSeq(q)
		if ref != nil {
			return "", nil, ref
		}
		query, args := h.eventQuery(op)
		out, err := pageRows(ctx, tx, q, limit, after, query, scanEvent(q.epoch), args...)
		return "", out, err
	})
}

const (
	streamBatch = 100
	streamPoll  = 500 * time.Millisecond
	// streamKeepAlive is how long a stream stays silent before it sends a comment line, so a
	// proxy's idle limit does not cut it (§8.3).
	streamKeepAlive = 15 * time.Second
)

// idleTimer is an event stream's keep-alive timer: c ticks once the stream has sent nothing for
// the interval, rearm starts the interval again, and stop releases the timer.
type idleTimer struct {
	c     <-chan time.Time
	rearm func()
	stop  func() bool
}

func newIdleTimer(d time.Duration) idleTimer {
	t := time.NewTimer(d)
	return idleTimer{c: t.C, rearm: func() { t.Reset(d) }, stop: t.Stop}
}

// streamEvents answers the operation's events as Server-Sent Events (§8.3): each event's id is
// `<epoch>:<number>`; a Last-Event-ID of another epoch cannot be resumed, so the server sends a
// reset event and the events from the start. The stream ends when the operation is terminal and
// its events are written, or no later than the expiry of the token that opened it, so a revocation
// takes effect on a stream within the token's lifetime and a resumption is verified as any request.
func streamEvents(a *API, w http.ResponseWriter, q *request) {
	op := q.r.PathValue("id")
	var lastEpoch string
	var last int64
	if v := q.r.Header.Get("Last-Event-ID"); v != "" {
		ep, n, ok := strings.Cut(v, ":")
		num, err := strconv.ParseInt(n, 10, 64)
		if !ok || id.MustHave(ep, id.Epoch) != nil || err != nil || num < 0 {
			a.fail(w, q, refuse(http.StatusBadRequest, "invalid-request", "Last-Event-ID is not an event identifier"))
			return
		}
		lastEpoch, last = ep, num
	}
	// The token's expiry bounds every read and write of the stream, the first read included: its
	// context ends the reads, its write deadline a blocked write, and each frame is checked before
	// it is written.
	rc := http.NewResponseController(w)
	ctx := q.r.Context()
	if exp := q.principal.Expiry; !exp.IsZero() {
		_ = rc.SetWriteDeadline(exp)
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, exp)
		defer cancel()
		q.r = q.r.WithContext(ctx)
	}
	var reset bool
	pass := func(first bool) (h opHead, batch []eventBody, seqs []int64, err error) {
		err = readTx(a, q, func(ctx context.Context, tx *sql.Tx) error {
			if first && lastEpoch != "" && lastEpoch != q.epoch {
				reset, last = true, 0
			}
			if h, err = readHead(ctx, tx, op); err != nil {
				return err
			}
			// The state is read before the events: a terminal state is written after the last entry.
			query, args := h.eventQuery(op)
			rows, err := tx.QueryContext(ctx, query, append([]any{strconv.FormatInt(last, 10), streamBatch}, args...)...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				e, num, err := scanEvent(q.epoch)(rows)
				if err != nil {
					return err
				}
				n, _ := strconv.ParseInt(num, 10, 64)
				batch, seqs = append(batch, e), append(seqs, n)
			}
			return rows.Err()
		})
		return h, batch, seqs, err
	}
	h, batch, seqs, err := pass(true)
	setEpoch(w, q)
	if err != nil {
		a.fail(w, q, err)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if reset {
		_, _ = fmt.Fprint(w, "event: reset\ndata: {}\n\n")
	}
	// A stream with no event to send for streamKeepAlive sends a comment line, which carries no id
	// and no event, so neither the client's Last-Event-ID nor its event handling sees it.
	timer := a.o.keepAlive
	if timer == nil {
		timer = newIdleTimer
	}
	idle := timer(streamKeepAlive)
	defer idle.stop()
	for {
		for i, e := range batch {
			if ctx.Err() != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", e.ID, e.Type, e.Data); err != nil {
				return
			}
			last = seqs[i]
		}
		if len(batch) > 0 {
			idle.rearm()
		}
		_ = rc.Flush()
		if len(batch) < streamBatch {
			if h.terminal() {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-idle.c:
				if ctx.Err() != nil {
					return
				}
				if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
					return
				}
				_ = rc.Flush()
				idle.rearm()
			case <-time.After(streamPoll):
			}
		}
		if h, batch, seqs, err = pass(false); err != nil {
			return // the headers are sent: ending the stream is the answer, and the client resumes
		}
	}
}

// timelineEntry is one entry of a machine's timeline (execution-recovery.md §4.1).
type timelineEntry struct {
	Revision int64           `json:"revision"`
	Epoch    string          `json:"epoch"`
	At       time.Time       `json:"at"`
	Kind     string          `json:"kind"`
	Entry    json.RawMessage `json:"entry"`
}

// listTimeline is GET /machines/{id}/timeline: the machine's entries in revision order, without a
// response's text or an artifact digest.
func listTimeline(a *API, w http.ResponseWriter, q *request) {
	m := q.r.PathValue("id")
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		if err := machineNamed(ctx, tx, m); err != nil {
			return "", nil, err
		}
		limit, after, ref := pageSeq(q)
		if ref != nil {
			return "", nil, ref
		}
		out, err := pageRows(ctx, tx, q, limit, after, `SELECT revision, epoch, at, kind, (entry - 'text' - 'digest')::text
			FROM machine_event WHERE machine = $3 AND revision > $1::text::bigint ORDER BY revision LIMIT $2`,
			func(r *sql.Rows) (timelineEntry, string, error) {
				var e timelineEntry
				var entry string
				if err := r.Scan(&e.Revision, &e.Epoch, &e.At, &e.Kind, &entry); err != nil {
					return e, "", err
				}
				e.At, e.Entry = e.At.UTC(), json.RawMessage(entry)
				return e, strconv.FormatInt(e.Revision, 10), nil
			}, m)
		return "", out, err
	})
}
