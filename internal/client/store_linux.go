package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
	ss "github.com/zalando/go-keyring/secret_service"
)

// keyringUnavailable is why the Secret Service is not used, "" when it is (decisions T3, T4 of
// #254). Only a session bus the environment names is used: the D-Bus library would otherwise find
// the login session's bus by itself or start one. A named bus on which no program offers the
// Secret Service, as on many SSH logins, is no store either; one that cannot be asked is used, so
// that its failure is shown.
func keyringUnavailable() string {
	if a := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); a == "" || a == "autolaunch:" {
		return "no D-Bus session bus is named (DBUS_SESSION_BUS_ADDRESS)"
	}
	if offered, err := secretServiceOffered(); err == nil && !offered {
		return "no program on the D-Bus session bus offers the Secret Service (" + ssName + ")"
	}
	return ""
}

// secretServiceOffered reports whether a program on the session bus owns the Secret Service's
// name, or the bus can start one for it (D-Bus activation). Replaced in tests.
var secretServiceOffered = func() (bool, error) {
	return within(func() (bool, error) {
		c, err := dbus.ConnectSessionBus()
		if err != nil {
			return false, err
		}
		defer c.Close()
		var owned bool
		if err := c.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, ssName).Store(&owned); err != nil || owned {
			return owned, err
		}
		var names []string
		if err := c.BusObject().Call("org.freedesktop.DBus.ListActivatableNames", 0).Store(&names); err != nil {
			return false, err
		}
		return slices.Contains(names, ssName), nil
	})
}

// unreached says, after a sentence, that a sign-in another session kept in the Secret Service is
// not reached without a session bus: it is neither removed nor revoked.
func unreached() string {
	return " A sign-in another session kept in the system credential store is not reached, so it is neither removed nor revoked."
}

// keyringGet reads through go-keyring, whose calls wait on the store's unlock prompt: it is used
// only outside the renewal lock, and within bounds the wait.
func keyringGet(user string) (string, bool, error) {
	return bounded(keyringGetCall, user)
}

// keyringGetHeld is keyringGet under the renewal lock: a locked store is refused, never prompted.
func keyringGetHeld(user string) (string, bool, error) {
	return bounded(keyringGetHeldCall, user)
}

func bounded(get func(string, string) (string, error), user string) (string, bool, error) {
	v, err := within(func() (*string, error) {
		v, err := get(keyringService, user)
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, nil
		}
		return &v, err
	})
	if err != nil || v == nil {
		return "", false, err
	}
	return *v, true, nil
}

// The Secret Service's calls, replaced in tests: go-keyring's prompting read, and the calls made
// under the renewal lock, which never prompt.
var (
	keyringGetCall                                        = keyring.Get
	keyringGetHeldCall, keyringSetCall, keyringDeleteCall = ssGet, ssSet, ssDelete
)

// keyringSet and keyringDelete are bounded by the Secret Service calls' own deadline (ssBound),
// not by within, which would leave the call running after it gave up.
func keyringSet(user, v string) error {
	return keyringSetCall(keyringService, user, v)
}

func keyringDelete(user string) error {
	if err := keyringDeleteCall(keyringService, user); !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

const (
	ssName  = "org.freedesktop.secrets"
	ssPath  = dbus.ObjectPath("/org/freedesktop/secrets")
	ssAlias = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")
	ssLogin = dbus.ObjectPath("/org/freedesktop/secrets/collection/login")
	ssIface = "org.freedesktop.Secret."
)

// secretService runs f on the collection go-keyring v0.2.8 keeps entries in (its
// GetLoginCollection: "login" when the service lists it, else the default alias), with the items,
// label and attributes go-keyring uses, so that each reads what the other wrote. Unlike
// go-keyring's calls it never shows a prompt, which they wait on for as long as the operator
// leaves it: a collection that is locked, or any answer that needs a prompt, is refused
// (errStoreLocked).
//
// Its calls together get ssBound (§9.2 item 3): neither the session bus nor the D-Bus library
// ends a call that gets no answer (ginsys/bronzeward#437). At the deadline the connection closes,
// which fails the call in flight, and the operation returns errStoreSlow.
func secretService(f func(c *dbus.Conn, col dbus.BusObject) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), ssBound)
	defer cancel()
	err := func() error {
		c, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
		if err != nil {
			return err
		}
		defer c.Close()
		path := ssLogin
		v, err := c.Object(ssName, ssPath).GetProperty(ssIface + "Service.Collections")
		if listed, _ := v.Value().([]dbus.ObjectPath); err != nil || !slices.Contains(listed, path) {
			path = ssAlias
		}
		if err := ssUnlocked(c, path); err != nil {
			return err
		}
		return f(c, c.Object(ssName, path))
	}()
	if err != nil && ctx.Err() != nil {
		return errStoreSlow
	}
	return err
}

// ssBound is how long one Secret Service operation under the renewal lock waits for its answers.
var ssBound = time.Minute

// errStoreSlow: the Secret Service did not answer within ssBound, before any write was sent.
var errStoreSlow = errors.New("the system credential store did not answer within a minute; nothing was written or removed")

// unsettled is the error of an operation whose write, sent, got no answer within ssBound: the
// Secret Service may still carry it out after the renewal lock is released.
func unsettled(err error, sent bool, what string) error {
	if sent && errors.Is(err, errStoreSlow) {
		return fmt.Errorf("the system credential store did not answer within a minute; the sign-in may still be %s after this command ends", what)
	}
	return err
}

// ssUnlocked asks the Secret Service to unlock p, which an unlocked object needs no prompt for,
// and refuses one that does; the check on the answer is go-keyring's.
func ssUnlocked(c *dbus.Conn, p dbus.ObjectPath) error {
	var done []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := c.Object(ssName, ssPath).Call(ssIface+"Service.Unlock", 0, []dbus.ObjectPath{p}).Store(&done, &prompt); err != nil {
		return err
	}
	if err := ssNoPrompt(c, prompt); err != nil {
		return err
	}
	if len(done) != 1 || (p != ssAlias && done[0] != p) {
		return errors.New("the system credential store did not unlock the collection it keeps sign-ins in")
	}
	return nil
}

// ssNoPrompt refuses an answer that needs a prompt. The prompt is dismissed, never shown.
func ssNoPrompt(c *dbus.Conn, prompt dbus.ObjectPath) error {
	if prompt == "/" {
		return nil
	}
	c.Object(ssName, prompt).Call(ssIface+"Prompt.Dismiss", dbus.FlagNoReplyExpected)
	return errStoreLocked
}

func ssSession(c *dbus.Conn) (dbus.ObjectPath, error) {
	var out dbus.Variant
	var session dbus.ObjectPath
	err := c.Object(ssName, ssPath).Call(ssIface+"Service.OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &session)
	return session, err
}

func ssFind(col dbus.BusObject, service, user string) (dbus.ObjectPath, error) {
	var found []dbus.ObjectPath
	if err := col.Call(ssIface+"Collection.SearchItems", 0, map[string]string{"username": user, "service": service}).Store(&found); err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", keyring.ErrNotFound
	}
	return found[0], nil
}

func ssGet(service, user string) (string, error) {
	var v string
	err := secretService(func(c *dbus.Conn, col dbus.BusObject) error {
		item, err := ssFind(col, service, user)
		if err != nil {
			return err
		}
		if err := ssUnlocked(c, item); err != nil {
			return err
		}
		session, err := ssSession(c)
		if err != nil {
			return err
		}
		var s ss.Secret
		if err := c.Object(ssName, item).Call(ssIface+"Item.GetSecret", 0, session).Store(&s); err != nil {
			return err
		}
		v = string(s.Value)
		return nil
	})
	return v, err
}

func ssSet(service, user, pass string) error {
	sent := false
	err := secretService(func(c *dbus.Conn, col dbus.BusObject) error {
		session, err := ssSession(c)
		if err != nil {
			return err
		}
		props := map[string]dbus.Variant{
			ssIface + "Item.Label":      dbus.MakeVariant(fmt.Sprintf("Password for '%s' on '%s'", user, service)),
			ssIface + "Item.Attributes": dbus.MakeVariant(map[string]string{"username": user, "service": service}),
		}
		var item, prompt dbus.ObjectPath
		sent = true
		if err := col.Call(ssIface+"Collection.CreateItem", 0, props, ss.NewSecret(session, pass), true).Store(&item, &prompt); err != nil {
			return err
		}
		return ssNoPrompt(c, prompt)
	})
	return unsettled(err, sent, "written")
}

func ssDelete(service, user string) error {
	sent := false
	err := secretService(func(c *dbus.Conn, col dbus.BusObject) error {
		item, err := ssFind(col, service, user)
		if err != nil {
			return err
		}
		var prompt dbus.ObjectPath
		sent = true
		if err := c.Object(ssName, item).Call(ssIface+"Item.Delete", 0).Store(&prompt); err != nil {
			return err
		}
		return ssNoPrompt(c, prompt)
	})
	return unsettled(err, sent, "removed")
}
