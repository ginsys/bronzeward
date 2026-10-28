package client

import (
	"errors"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
)

// keyringUnavailable is why the Secret Service is not used, "" when it is (decisions T3, T4 of
// #254). Only a session bus the environment names is used: the D-Bus library would otherwise find
// the login session's bus by itself or start one.
func keyringUnavailable() string {
	if a := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); a == "" || a == "autolaunch:" {
		return "no D-Bus session bus is named (DBUS_SESSION_BUS_ADDRESS)"
	}
	return ""
}

// unreached says, after a sentence, that a sign-in another session kept in the Secret Service is
// not reached without a session bus: it is neither removed nor revoked.
func unreached() string {
	return " A sign-in another session kept in the system credential store is not reached, so it is neither removed nor revoked."
}

// keyringLocked reports whether the collection go-keyring keeps entries in ("login", else the
// default alias) is locked, asking the Secret Service without an unlock prompt. Replaced in tests.
var keyringLocked = func() (bool, error) {
	return within(func() (bool, error) {
		c, err := dbus.ConnectSessionBus()
		if err != nil {
			return false, err
		}
		defer c.Close()
		for _, p := range []dbus.ObjectPath{"/org/freedesktop/secrets/collection/login", "/org/freedesktop/secrets/aliases/default"} {
			v, err := c.Object("org.freedesktop.secrets", p).GetProperty("org.freedesktop.Secret.Collection.Locked")
			if err != nil {
				continue
			}
			locked, ok := v.Value().(bool)
			if !ok {
				return false, errors.New("the system credential store answered whether it is locked with something else than a yes or no")
			}
			return locked, nil
		}
		// No collection: go-keyring's call fails by itself, without a prompt.
		return false, nil
	})
}

func keyringGet(user string) (string, bool, error) {
	v, err := within(func() (*string, error) {
		v, err := keyringGetCall(keyringService, user)
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

// The Secret Service's calls, replaced in tests.
var keyringGetCall, keyringSetCall, keyringDeleteCall = keyring.Get, keyring.Set, keyring.Delete

// keyringSet and keyringDelete are not bounded by within: a write given up on would still land
// after its caller released the renewal lock, over a newer sign-in or one cleared meanwhile. So a
// store that stops answering mid-write holds the lock, and blocks other bronzewardctl commands on
// that context, until it answers. They run only under the lock, on a store found unlocked
// (store.lock), so that the wait is not an unlock prompt.
func keyringSet(user, v string) error {
	return keyringSetCall(keyringService, user, v)
}

func keyringDelete(user string) error {
	if err := keyringDeleteCall(keyringService, user); !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}
