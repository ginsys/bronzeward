package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/role"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// latestSigner is the control of persistence-api.md §3.4's "never from latest": a Signer that
// answers a named-version read with the latest version, as a signer that ignored the act would.
type latestSigner struct{ Signer }

func (s latestSigner) ReadCAKeyVersion(ctx context.Context, cluster string, _ int) (provider.CAKey, error) {
	return s.ReadCAKey(ctx, cluster)
}

// PA §3.3, §3.4: a certificate is signed from the exact CA key version the cluster's latest
// first-key act names. A version written after it, which no act names, is never signed from; the
// control answers the latest for the named version and is refused as a version written again.
func TestCertForSignsFromTheVersionTheActNames(t *testing.T) {
	t.Parallel()
	me := newMachineEnv(t)
	a := me.build(options{})
	ctx := t.Context()
	first, cause, err := a.certFor(ctx, me.cluster, role.Admin, time.Minute)
	if err != nil || cause != "" || !sameVersion(first.CA(), me.keyAct) {
		t.Fatalf("the act's version: %+v, cause %q, %v; want %+v", first.CA(), cause, err, me.keyAct)
	}
	if first.Serial() == "" || !first.NotAfter().After(time.Now().Add(50*time.Second)) {
		t.Fatalf("the certificate: serial %q, NotAfter %v", first.Serial(), first.NotAfter())
	}

	// Version 2 exists and no act names it: version 1 is still the one signed from, and no read
	// asked for the latest.
	me.keys.kv.put(me.keyAct.Path, string(me.keys.ca.Crt), string(me.keys.ca.Key))
	c, cause, err := a.certFor(ctx, me.cluster, role.Admin, time.Minute)
	if err != nil || cause != "" || c.CA().Version != 1 {
		t.Fatalf("with version 2 written and no act: %+v, cause %q, %v", c.CA(), cause, err)
	}
	for _, v := range me.keys.kv.readVersions() {
		if v != 1 {
			t.Fatalf("reads of the CA key asked for versions %v; every one must name version 1", me.keys.kv.readVersions())
		}
	}

	// The control: a signer that answers the latest for the named version is refused, and nothing is signed.
	control := me.build(options{})
	control.d.sg = latestSigner{me.keys.sg}
	if c, cause, err := control.certFor(ctx, me.cluster, role.Admin, time.Minute); err == nil || cause != "version-changed" || c.Serial() != "" {
		t.Fatalf("a signer that reads the latest: serial %q, cause %q, %v; want version-changed and no certificate", c.Serial(), cause, err)
	}

	// An act that names version 2 moves the next certificate to it, with its own serial.
	moved := me.keys.seed(t, me.db, me.cluster)
	if moved.Version != 3 {
		t.Fatalf("the seeded version is %d; the stand-in held two", moved.Version)
	}
	next, cause, err := a.certFor(ctx, me.cluster, role.Admin, time.Minute)
	if err != nil || cause != "" || !sameVersion(next.CA(), moved) || next.Serial() == first.Serial() {
		t.Fatalf("after the replacement: %+v serial %q, cause %q, %v; want %+v and a new serial", next.CA(), next.Serial(), cause, err, moved)
	}
}

// PA §3.3, §3.4: the causes a certificate is refused with, each a talos-access-unavailable cause.
func TestCertForRefusals(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		cause string
		set   func(t *testing.T, me *machineEnv, a *API)
		life  time.Duration
	}{
		"no act names a key": {"absent", func(t *testing.T, me *machineEnv, _ *API) {
			me.noSeed = true // the refusal is proven with no act, and signer() seeds none
			dropFirstKeyActs(t, me.db, me.cluster)
		}, time.Minute},
		"the named version is gone": {"version-gone", func(_ *testing.T, me *machineEnv, _ *API) {
			me.keys.kv.destroy(me.keyAct.Path, me.keyAct.Version)
		}, time.Minute},
		"the named version was written again": {"version-changed", func(_ *testing.T, me *machineEnv, _ *API) {
			me.keys.kv.mu.Lock()
			defer me.keys.kv.mu.Unlock()
			me.keys.kv.versions[me.keyAct.Path][0].created = me.keyAct.CreatedTime.Add(time.Hour)
		}, time.Minute},
		"the provider is sealed": {"provider-unavailable", func(_ *testing.T, me *machineEnv, _ *API) {
			me.keys.kv.mu.Lock()
			defer me.keys.kv.mu.Unlock()
			me.keys.kv.status["GET"] = 503
		}, time.Minute},
		"the lifetime is above the maximum": {"lifetime-exceeds-maximum", func(_ *testing.T, _ *machineEnv, a *API) {
			a.d.maxCert = 10 * time.Second
		}, 30 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			me := newMachineEnv(t)
			a := me.build(options{})
			c.set(t, me, a)
			cert, cause, err := a.certFor(t.Context(), me.cluster, role.Admin, c.life)
			if err == nil || cause != c.cause || cert.Serial() != "" {
				t.Fatalf("cause %q, serial %q, error %v; want %q and no certificate", cause, cert.Serial(), err, c.cause)
			}
		})
	}
}

// heldReader is a node connection that answers nothing and ignores every request context: it
// returns only when it is closed, as a connection held open past its certificate would.
type heldReader struct {
	closed chan struct{}
	once   sync.Once
}

func newHeldReader() *heldReader { return &heldReader{closed: make(chan struct{})} }

func (h *heldReader) hold() error {
	<-h.closed
	return errors.New("the connection was closed")
}

func (h *heldReader) Identity(context.Context) (talos.Identity, error) {
	return talos.Identity{}, h.hold()
}
func (h *heldReader) Version(context.Context) (string, error) { return "", h.hold() }
func (h *heldReader) MachineConfig(context.Context) (talos.Config, error) {
	return talos.Config{}, h.hold()
}
func (h *heldReader) Close() error {
	h.once.Do(func() { close(h.closed) })
	return nil
}

// PA §3.3, §3.4: no connection outlives its certificate. A connection that answers nothing is closed
// at the certificate's NotAfter and the observation is recorded without a node value; the control
// reads through the stored talosconfig, which no certificate bounds, and the same connection is
// still held after that NotAfter has passed.
func TestObserveHeldConnectionEndsAtNotAfter(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, access bool) (h *heldReader, done chan error, oe *obsEnv, started time.Time) {
		t.Helper()
		oe = newObsEnv(t)
		h = newHeldReader()
		dial := func(context.Context, []byte, string) (talos.Reader, error) { return h, nil }
		a := observer(t, oe.env, oe.x, options{dial: dial, nodeTimeout: 250 * time.Millisecond})
		f := observeFor{purpose: "drift"}
		if access {
			f.access = &oe.x.access
		}
		done = make(chan error, 1)
		started = time.Now()
		go func() { _, err := a.observe(t.Context(), oe.machine, f); done <- err }()
		return h, done, oe, started
	}

	t.Run("certificate", func(t *testing.T) {
		t.Parallel()
		h, done, oe, started := run(t, false)
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			h.Close()
			t.Fatal("the observation was still waiting on the held connection after its certificate's NotAfter")
		}
		var notAfter time.Time
		if err := oe.db.QueryRow(`SELECT access_not_after FROM observation WHERE machine = $1`, oe.machine).Scan(&notAfter); err != nil {
			t.Fatal(err)
		}
		if ended := time.Now(); ended.After(notAfter.Add(time.Second)) {
			t.Fatalf("the observation ended at %v, more than a second after the certificate's NotAfter %v (started %v)", ended, notAfter, started)
		}
		select {
		case <-h.closed:
		default:
			t.Fatal("the connection was not closed")
		}
		if r := readObs(t, oe.db, oe.machine); r.unread["identity"]["cause"] == "" || r.digest != nil {
			t.Fatalf("the observation read a value through a held connection: %+v", r)
		}
	})

	t.Run("control: the stored talosconfig has no certificate to end it", func(t *testing.T) {
		t.Parallel()
		h, done, _, _ := run(t, true)
		select {
		case <-done:
			t.Fatal("the control ended by itself: it does not hold a connection past a NotAfter")
		case <-time.After(4 * time.Second): // past the end a certificate for this observation would have had
		}
		h.Close()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

// PA §3.3: a key replaced between two observations is picked up by the second, which records the
// new version, its own serial and its own end; the first stays as it was recorded.
func TestObservationsFollowAReplacedKey(t *testing.T) {
	t.Parallel()
	oe := newObsEnv(t)
	a := observer(t, oe.env, oe.x, options{})
	if _, err := a.observe(t.Context(), oe.machine, observeFor{purpose: "drift"}); err != nil {
		t.Fatal(err)
	}
	second := oe.keys.seed(t, oe.db, oe.cluster)
	if _, err := a.observe(t.Context(), oe.machine, observeFor{purpose: "drift"}); err != nil {
		t.Fatal(err)
	}
	rows, err := oe.db.Query(`SELECT access_path, access_version, access_created, access_serial, access_not_after
		FROM observation WHERE machine = $1 ORDER BY revision`, oe.machine)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []observedAccess
	for rows.Next() {
		var x observedAccess
		var created string
		if err := rows.Scan(&x.Path, &x.Version, &created, &x.Serial, &x.NotAfter); err != nil {
			t.Fatal(err)
		}
		if x.CreatedTime, err = time.Parse(time.RFC3339Nano, created); err != nil {
			t.Fatal(err)
		}
		got = append(got, x)
	}
	if len(got) != 2 || !got[0].signedFrom(oe.keyAct) || !got[1].signedFrom(second) || got[0].Serial == got[1].Serial {
		t.Fatalf("the two observations name %+v; want one from %+v, then another, with its own serial, from %+v", got, oe.keyAct, second)
	}
}

// A database failure while looking for the cluster's act is an error and no credential cause: neither
// an observation nor an ingestion records it as a talos-access-unavailable failure (the context is
// live, so nothing else would stop them).
func TestCertForDatabaseFailureIsNoCause(t *testing.T) {
	t.Run("observation", func(t *testing.T) {
		oe := newObsEnv(t)
		a := observer(t, oe.env, oe.x, options{})
		mustExec(t, oe.db, "ALTER TABLE first_key_act RENAME TO first_key_act_away")
		t.Cleanup(func() { mustExec(t, oe.db, "ALTER TABLE first_key_act_away RENAME TO first_key_act") })
		if _, cause, err := a.certFor(t.Context(), oe.cluster, role.Admin, time.Minute); err == nil || cause != "" {
			t.Fatalf("certFor = cause %q, %v; want an error and no cause", cause, err)
		}
		if _, err := a.observe(t.Context(), oe.machine, observeFor{purpose: "drift"}); err == nil {
			t.Fatal("observe recorded a database failure as a talos-access-unavailable observation")
		}
		if n := count(t, oe.db, `SELECT count(*) FROM observation WHERE machine = $1`, oe.machine); n != 0 {
			t.Fatalf("%d observations recorded; want none", n)
		}
	})
	t.Run("ingestion", func(t *testing.T) {
		me := newMachineEnv(t)
		mustExec(t, me.db, "ALTER TABLE first_key_act RENAME TO first_key_act_away")
		t.Cleanup(func() { mustExec(t, me.db, "ALTER TABLE first_key_act_away RENAME TO first_key_act") })
		op, j := me.startJob(t, map[string]any{"source": "machine", "document": nil})
		me.runWith(t, options{nodeTimeout: 500 * time.Millisecond}, j)
		if r := readOp(t, me.db, op); r.state == "failed" {
			t.Fatalf("a database failure ended the ingestion as a refusal: %+v", r.error)
		}
	})
}
