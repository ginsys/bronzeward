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

func keyringSet(user, v string) error {
	_, err := within(func() (struct{}, error) { return struct{}{}, keyring.Set(keyringService, user, v) })
	return err
}

func keyringDelete(user string) error {
	_, err := within(func() (struct{}, error) {
		if err := keyring.Delete(keyringService, user); !errors.Is(err, keyring.ErrNotFound) {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}
