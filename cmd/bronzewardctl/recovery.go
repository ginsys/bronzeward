package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// recoveryCmd is recovery status, enter and exit (#361; operator-experience.md §5.9, §10). The
// accounting, takeover, resolution, mark and release are #362's.
func (e *env) recoveryCmd(args []string) error {
	v, rest, err := e.verb("recovery", args, "status", "enter", "exit")
	if err != nil {
		return err
	}
	return map[string]func([]string) error{"status": e.recoveryStatus, "enter": e.recoveryEnter, "exit": e.recoveryExit}[v](rest)
}

// recoveryStatus is T31: GET /recovery with the start mode GET /server showed, and one row per scope
// in recovery with its next step.
func (e *env) recoveryStatus(args []string) error {
	if _, err := e.parse("recovery status", args, 0, nil); err != nil {
		return err
	}
	return e.session(func(c *client.Client, _ *client.Response, srv *apitypes.ServerBody) error {
		r, err := c.Get("/recovery")
		if err != nil {
			return err
		}
		recs, err := decode[apitypes.RecoveryBody](c, r.Body)
		if err != nil {
			return err
		}
		rec, released := recs[0], 0
		var rows []string
		for _, s := range rec.Scopes {
			if s.State == "normal" {
				continue
			}
			if s.State == "released" {
				released++
			}
			rows = append(rows, s.Machine+"\t"+s.State+"\t"+nextStep(c, s))
		}
		return e.emit(r.Body, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "Epoch\t%s\n", rec.Epoch)
			fmt.Fprintf(w, "Recovery mode\t%s\n", map[bool]string{true: "on", false: "off"}[rec.RecoveryMode])
			fmt.Fprintf(w, "Start mode\t%s (GET /server)\n", srv.StartMode)
			if en := rec.Entry; en != nil {
				fmt.Fprintf(w, "Entered\t%s by %s: %s\n", when(en.EnteredAt), en.EnteredBy, en.Reason)
				fmt.Fprintf(w, "Restore stopped\t%s\n", when(en.StoppedAt))
				fmt.Fprintf(w, "Restored\t%s\n", restoredList(en.Restored))
			}
			if x := rec.Exit; x != nil {
				fmt.Fprintf(w, "Exited\t%s by %s (act %s)\n", when(x.At), x.ExitedBy, x.Act)
			}
			if len(rows) == 0 {
				fmt.Fprintln(w, "Scopes\tnone in recovery")
				return
			}
			fmt.Fprintln(w, "\nMACHINE\tSCOPE STATE\tNEXT")
			for _, row := range rows {
				fmt.Fprintln(w, row)
			}
			if released == len(rows) && rec.RecoveryMode {
				fmt.Fprintln(w, "\nEvery scope is released: recovery exit leaves recovery mode.")
			}
		})
	})
}

// nextStep is T31's next step for a scope in state s.State, with the reads it needs: the holding
// operation of an unresolved scope, and the last mark of a blocked one. A failed read names the
// command that shows it.
func nextStep(c *client.Client, s apitypes.RecoveryScope) string {
	m := client.ShellWord(s.Machine)
	switch s.State {
	case "pre-restore-unaccounted":
		return "account the scope: recovery scope-account " + m
	case "ready":
		return "release: recovery release " + m
	case "released":
		return "none"
	case "unresolved":
		items, err := c.List("/operations?" + url.Values{"machine": {s.Machine}, "state": {"unresolved"}}.Encode())
		ops, derr := decode[apitypes.OperationBody](c, items...)
		if err != nil || derr != nil || len(ops) == 0 {
			return "an operation holds it: operation list --machine " + m
		}
		r, err := c.Get("/operations/" + url.PathEscape(ops[0].Id))
		var op apitypes.OperationBody
		if err != nil || json.Unmarshal(r.Body, &op) != nil {
			return "operation " + ops[0].Id + " holds it: operation show " + ops[0].Id
		}
		if op.Attempts != nil && len(*op.Attempts) > 0 {
			return "operation " + op.Id + " holds it, with an attempt: classified after the normal restart, then recovery mark " + m
		}
		return "operation " + op.Id + " holds it, with no attempt: recovery resolve " + op.Id
	case "blocked":
		r, err := c.Get("/machines/" + url.PathEscape(s.Machine) + "/timeline?order=newest&limit=100")
		var page struct{ Items []apitypes.TimelineEntry }
		if err != nil || json.Unmarshal(r.Body, &page) != nil {
			return "see machine timeline " + m + ", then recovery mark " + m
		}
		for _, t := range page.Items {
			x, _ := t.Entry.(map[string]any)
			if t.Kind != "scope-state" || x["to"] != "blocked" {
				continue
			}
			var missing []string
			for _, v := range asList(x["missing"]) {
				missing = append(missing, fmt.Sprint(v))
			}
			if x["cause"] != "restoration-mark" || len(missing) == 0 {
				break
			}
			return "missing " + strings.Join(missing, ", ") + ": then recovery mark " + m + " again, or a new release for a lost key version"
		}
		return "not marked since it was blocked: recovery mark " + m
	}
	return "see machine timeline " + m
}

func asList(v any) []any { l, _ := v.([]any); return l }

func restoredList(rs []apitypes.RestoredBackup) string {
	var s []string
	for _, b := range rs {
		s = append(s, fmt.Sprintf("%s backup %s taken at %s", b.Family, b.Backup, when(b.TakenAt)))
	}
	return strings.Join(s, "; ")
}

// restoredFlag is --restored family=<f>,backup=<id>,taken-at=<time>, given once per family.
type restoredFlag []apitypes.RestoredBackup

func (r *restoredFlag) String() string { return "" }

func (r *restoredFlag) Set(s string) error {
	var b apitypes.RestoredBackup
	for _, kv := range strings.Split(s, ",") {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "family":
			b.Family = apitypes.RestoredBackupFamily(v)
		case "backup":
			b.Backup = v
		case "taken-at":
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return errors.New("taken-at: give an RFC 3339 time, such as 2026-11-01T08:30:00Z")
			}
			b.TakenAt = t
		default:
			return fmt.Errorf("%q: give family=<f>,backup=<id>,taken-at=<time>", kv)
		}
	}
	if b.Family == "" || b.Backup == "" || b.TakenAt.IsZero() {
		return errors.New("give family, backup and taken-at, all three")
	}
	*r = append(*r, b)
	return nil
}

// factsFlag is --fact name=value, given once per fact.
type factsFlag map[string]string

func (f *factsFlag) String() string { return "" }

func (f *factsFlag) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return errors.New("give name=value")
	}
	if *f == nil {
		*f = factsFlag{}
	}
	(*f)[k] = v
	return nil
}

// entryRepeat is T10's repeat of the recovery entry: its key and principal, never an epoch, since
// the entry is sent without one (client-contracts.md §8.4 items 3 and 4).
func (r *repeatFlags) entryRepeat() (*client.Repeat, error) {
	for name, g := range map[string]given{"idempotency-key": r.key, "expect-principal": r.principal} {
		if g.set && g.v == "" {
			return nil, usageError{fmt.Errorf("--%s was given an empty value: give it as printed; nothing was sent", name)}
		}
	}
	switch {
	case r.epoch.set || r.ifMatch.set:
		return nil, usageError{errors.New("the recovery entry is sent without an epoch or If-Match, so its repeat takes only --idempotency-key and --expect-principal; nothing was sent")}
	case !r.key.set && !r.principal.set:
		return nil, nil
	case !r.key.set || !r.principal.set:
		return nil, usageError{errors.New("a repeat needs --idempotency-key and --expect-principal, each as the outcome-unknown block printed it; nothing was sent")}
	case !client.KeyShape.MatchString(r.key.v):
		return nil, usageError{errors.New("--idempotency-key: give the key as printed, 16 to 128 of A-Z, a-z, 0-9, - and _; nothing was sent")}
	}
	return &client.Repeat{Key: r.key.v, Principal: r.principal.v}, nil
}

// recoveryEnter is T32's entry (POST /recovery/entries, recovery-admin, human only, served by a
// recovery start only). It states the entry's effects, then sends it only when "enter" is typed
// back; there is no --yes.
func (e *env) recoveryEnter(args []string) error {
	var in apitypes.EntryRequest
	var restored restoredFlag
	var stopped string
	var rf repeatFlags
	if _, err := e.parse("recovery enter", args, 0, func(fs *flag.FlagSet) {
		fs.Var(&restored, "restored", "")
		fs.StringVar(&stopped, "stopped-at", "", "")
		fs.StringVar(&in.Reason, "reason", "", "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	t, err := time.Parse(time.RFC3339, stopped)
	switch {
	case len(restored) == 0 || len(restored) > 3:
		return usageError{errors.New("give --restored once per restored family, one to three times")}
	case err != nil:
		return usageError{errors.New("--stopped-at: give the time the restore stopped writes, in RFC 3339, such as 2026-11-01T08:30:00Z")}
	case strings.TrimSpace(in.Reason) == "":
		return usageError{errors.New("give --reason")}
	case e.noInput:
		return notConfirmed{errors.New("recovery enter asks for enter typed back, which --no-input forbids; nothing was sent")}
	}
	rp, err := rf.entryRepeat()
	if err != nil {
		return err
	}
	in.Restored, in.StoppedAt = restored, t
	body, _ := json.Marshal(in)
	confirm := func(c *client.Client) error {
		fmt.Fprintf(e.err, "Recovery entry in %s\n  restored:   %s\n  stopped at: %s\n  reason:     %s\n", c.Context.Where(), restoredList(restored), when(t), in.Reason)
		fmt.Fprint(e.err, "The entry mints a new epoch: every other client's pending write is refused.\n"+
			"It marks every machine's scope pre-restore-unaccounted.\n"+
			"It takes over every non-terminal apply-config operation as unresolved, and ends the running import and publish jobs.\n"+
			"Recovery mode then lasts until recovery exit, once every scope is released.\n")
		return e.confirmTyped("enter", "enter")
	}
	return e.writeAfter(confirm, &client.Write{Method: http.MethodPost, Path: "/recovery/entries", Body: body, HumanOnly: true, Entry: true,
		Repeat: rp, Check: "recovery status", Read: recoveryRead}, func(_ *client.Client, resp *client.Response) ([]string, error) {
		var x apitypes.EntryBody
		err := json.Unmarshal(resp.Body, &x)
		return []string{fmt.Sprintf("Recovery mode entered (act %s). Epoch %s, was %s.", x.Act, x.Epoch, x.PreviousEpoch),
			fmt.Sprintf("Scopes marked pre-restore-unaccounted: %d machines. Operations taken over or ended: %s.", x.Machines, count(x.Operations)),
			"Restart the server normally when ready; recovery mode continues.",
			"Next: recovery status."}, err
	})
}

// recoveryRead is what GET /recovery shows after an entry or exit of unknown outcome.
func recoveryRead(c *client.Client) (string, error) {
	r, err := c.Get("/recovery")
	if err != nil {
		return "", err
	}
	var x apitypes.RecoveryBody
	err = json.Unmarshal(r.Body, &x)
	s := fmt.Sprintf("GET /api/v1/recovery shows epoch %s, recovery mode %t", x.Epoch, x.RecoveryMode)
	if x.Entry != nil {
		s += ", entered at " + when(x.Entry.EnteredAt) + " by " + x.Entry.EnteredBy
	}
	if x.Exit != nil {
		s += ", exited at " + when(x.Exit.At)
	}
	return s, err
}

// recoveryExit is T32's exit (POST /recovery/exits, recovery-admin, human only). The server
// refuses it while a scope is not released and names the first one.
func (e *env) recoveryExit(args []string) error {
	var facts factsFlag
	var rf repeatFlags
	if _, err := e.parse("recovery exit", args, 0, func(fs *flag.FlagSet) {
		fs.Var(&facts, "fact", "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	in := apitypes.ExitRequest{}
	if facts != nil {
		in.Facts = (*map[string]string)(&facts)
	}
	body, _ := json.Marshal(in)
	err = e.write(&client.Write{Method: http.MethodPost, Path: "/recovery/exits", Body: body, HumanOnly: true, Repeat: rp,
		Check: "recovery status", Read: recoveryRead}, func(_ *client.Client, resp *client.Response) ([]string, error) {
		var x apitypes.ExitBody
		err := json.Unmarshal(resp.Body, &x)
		return []string{fmt.Sprintf("Recovery mode left (act %s) in epoch %s.", x.Act, x.Epoch),
			"Scopes returned to normal: " + count(x.Scopes) + "."}, err
	})
	var p *client.Problem
	if errors.As(err, &p) && p.Code() == "conflict" && p.Doc["machine"] != nil {
		return fmt.Errorf("%w\nThe first scope not released: machine %s, %s. Its next step: recovery status.", err,
			client.ShellWord(fmt.Sprint(p.Doc["machine"])), client.ShellWord(fmt.Sprint(p.Doc["scope"])))
	}
	return err
}

// identityCmd is identity revoke, T36 (POST /identity-revocations, recovery-admin, human only).
func (e *env) identityCmd(args []string) error {
	_, rest, err := e.verb("identity", args, "revoke")
	if err != nil {
		return err
	}
	var in apitypes.RevocationInput
	var identity, iss, sub string
	var rf repeatFlags
	if _, err := e.parse("identity revoke", rest, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&identity, "identity", "", "")
		fs.StringVar(&iss, "iss", "", "")
		fs.StringVar(&sub, "sub", "", "")
		fs.StringVar(&in.Reason, "reason", "", "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	target := identity
	switch {
	case identity != "" && (iss != "" || sub != ""), identity == "" && (iss == "" || sub == ""):
		return usageError{errors.New("name the identity with --identity, or with --iss and --sub, not both")}
	case strings.TrimSpace(in.Reason) == "":
		return usageError{errors.New("give --reason")}
	case e.noInput:
		return notConfirmed{errors.New("identity revoke asks for the identity typed back, which --no-input forbids; nothing was sent")}
	case identity != "":
		in.Identity = &identity
	default:
		in.Iss, in.Sub, target = &iss, &sub, sub
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(in)
	confirm := func(c *client.Client) error {
		fmt.Fprintf(e.err, "Revoking %s in %s. Reason: %s\n", client.ShellWord(target), c.Context.Where(), in.Reason)
		fmt.Fprint(e.err, "A revocation is permanent: no command undoes it.\n"+
			"It adds no auth.deniedSubjects entry and reissues no token, and a restore of the database can erase it.\n"+
			"Administrator steps (operator guide, recovery): first add the subject to auth.deniedSubjects, a human by iss and sub,\n"+
			"  a service identity by its identity and name, and leave it there; after a restore, record this revocation again.\n")
		return e.confirmTyped("the identity "+target, target)
	}
	w := &client.Write{Method: http.MethodPost, Path: "/identity-revocations", Body: body, HumanOnly: true, Repeat: rp, Check: "act list"}
	if identity != "" {
		w.Read = func(c *client.Client) (string, error) {
			return find(c, "/acts", "identity.revoke act for "+identity, func(a apitypes.ActItem) string {
				return map[bool]string{true: "the act " + a.Id + " revoking " + identity}[a.Action == "identity.revoke" && slices.Contains(a.Subjects, identity)]
			})
		}
	}
	return e.writeAfter(confirm, w, func(_ *client.Client, resp *client.Response) ([]string, error) {
		var x apitypes.RevocationBody
		err := json.Unmarshal(resp.Body, &x)
		listed := "yes"
		if !x.DeniedSubjectsListed {
			listed = "no: add it now, since a restore can erase this revocation"
		}
		return []string{fmt.Sprintf("Revoked %s (act %s).", x.Identity, x.Act), "auth.deniedSubjects lists it: " + listed + ".", x.Note}, err
	})
}
