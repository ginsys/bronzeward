//go:build !linux

package client

import "errors"

// keyringUnavailable: only Linux's Secret Service is supported until the packaging ticket
// verifies the other systems' stores (decision T3 of #254).
func keyringUnavailable() string {
	return "this system's credential store is not supported yet"
}

// unreached is empty: no sign-in is ever kept in this system's credential store.
func unreached() string { return "" }

var errNoKeyring = errors.New(keyringUnavailable())

func keyringGet(string) (string, bool, error) { return "", false, errNoKeyring }
func keyringSet(string, string) error         { return errNoKeyring }
func keyringDelete(string) error              { return errNoKeyring }
