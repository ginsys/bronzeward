package client

import (
	"errors"
	"os"

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

func keyringGet(user string) (string, bool, error) {
	v, err := within(func() (*string, error) {
		v, err := keyring.Get(keyringService, user)
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

// The Secret Service's writes, replaced in tests.
var keyringSetCall, keyringDeleteCall = keyring.Set, keyring.Delete

// keyringSet and keyringDelete are not bounded by within: a write given up on would still land
// after its caller released the renewal lock, over a newer sign-in or one cleared meanwhile. So a
// store that stops answering mid-write holds the lock, and blocks other bronzewardctl commands on
// that context, until it answers.
func keyringSet(user, v string) error {
	return keyringSetCall(keyringService, user, v)
}

func keyringDelete(user string) error {
	if err := keyringDeleteCall(keyringService, user); !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}
