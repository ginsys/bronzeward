package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/talos/discover"
)

// listedConfig is a talosconfig whose current context lists endpoints, as text of the file's own form.
func listedConfig(endpoints string) []byte {
	return []byte("context: a\ncontexts:\n  a:\n    endpoints: [" + endpoints + "]\n    ca: " + b64("ca") + "\n    crt: " + b64("crt") + "\n    key: " + b64("key") + "\n")
}

// eachFixture replaces the two reads discoverEach makes, one for an endpoint given by flag and one for each the
// file lists, and records the endpoints each was asked for. dead answers as an endpoint that does not answer.
func eachFixture(t *testing.T, dead ...string) (typed, listed *[]string) {
	t.Helper()
	typed, listed = new([]string), new([]string)
	read := func(into *[]string) func(context.Context, []byte, string, string) (discover.Result, error) {
		return func(_ context.Context, _ []byte, _ string, endpoint string) (discover.Result, error) {
			*into = append(*into, endpoint)
			if slices.Contains(dead, endpoint) {
				return discover.Result{}, errors.New("discover: version: Unavailable (" + endpoint + ")")
			}
			return discover.Result{ClusterID: "c-1", Endpoint: endpoint + ":50000"}, nil
		}
	}
	oldTyped, oldListed := discoverTyped, discoverListed
	discoverTyped, discoverListed = read(typed), read(listed)
	t.Cleanup(func() { discoverTyped, discoverListed = oldTyped, oldListed })
	return typed, listed
}

// TestDiscoverEachTriesTheListInOrder: for a list of three, every order of live and dead endpoints reads through
// the first live one and goes no further; all dead is one error that names each endpoint with its own cause.
func TestDiscoverEachTriesTheListInOrder(t *testing.T) {
	all := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	for mask := range 8 {
		var dead []string
		for i, e := range all {
			if mask>>i&1 == 1 {
				dead = append(dead, e)
			}
		}
		_, listed := eachFixture(t, dead...)
		res, answered, err := discoverEach(context.Background(), listedConfig(strings.Join(all, ", ")), "", "")
		first := -1
		for i, e := range all {
			if !slices.Contains(dead, e) {
				first = i
				break
			}
		}
		if first < 0 {
			if err == nil || !slices.Equal(*listed, all) {
				t.Errorf("pattern %d, all dead: error %v, asked %q, want an error and all three", mask, err, *listed)
				continue
			}
			for _, e := range all {
				if !strings.Contains(err.Error(), e) || !strings.Contains(err.Error(), "Unavailable ("+e+")") {
					t.Errorf("pattern %d: the error does not name %s with its own cause: %v", mask, e, err)
				}
			}
			continue
		}
		if err != nil || res.Endpoint != all[first]+":50000" || answered != all[first] || !slices.Equal(*listed, all[:first+1]) {
			t.Errorf("pattern %d: %+v, answered %q, %v, asked %q, want the first live endpoint %s (named as answering) after %d earlier tries", mask, res, answered, err, *listed, all[first], first)
		}
	}
}

// TestDiscoverEachTypedEndpointAlone: an endpoint given by flag is the only one tried, whatever the file lists.
func TestDiscoverEachTypedEndpointAlone(t *testing.T) {
	typed, listed := eachFixture(t, "10.9.9.9", "10.0.0.1", "10.0.0.2")
	_, _, err := discoverEach(context.Background(), listedConfig("10.0.0.1, 10.0.0.2"), "", "10.9.9.9")
	if err == nil || !slices.Equal(*typed, []string{"10.9.9.9"}) || len(*listed) != 0 {
		t.Errorf("error %v, typed %q, listed %q, want one try of the typed endpoint and none of the listed", err, *typed, *listed)
	}
}

// TestDiscoverEachOneListedEndpointKeepsItsOwnError: a list of one that fails gives that endpoint's own error, as
// the command gave it before a list was tried.
func TestDiscoverEachOneListedEndpointKeepsItsOwnError(t *testing.T) {
	eachFixture(t, "10.0.0.1")
	_, _, err := discoverEach(context.Background(), listedConfig("10.0.0.1"), "", "")
	if err == nil || err.Error() != "discover: version: Unavailable (10.0.0.1)" {
		t.Errorf("error %v, want the endpoint's own error alone", err)
	}
}

// TestDiscoverEachRefusals: no listed endpoint is ErrNoEndpoint and an unknown context is ErrUnknownContext, both
// before any read, so the commands keep their usage errors.
func TestDiscoverEachRefusals(t *testing.T) {
	typed, listed := eachFixture(t)
	if _, _, err := discoverEach(context.Background(), listedConfig(""), "", ""); !errors.Is(err, discover.ErrNoEndpoint) {
		t.Errorf("no endpoint: %v, want ErrNoEndpoint", err)
	}
	if _, _, err := discoverEach(context.Background(), listedConfig("10.0.0.1"), "c", ""); !errors.Is(err, discover.ErrUnknownContext) {
		t.Errorf("an unknown context: %v, want ErrUnknownContext", err)
	}
	if len(*typed)+len(*listed) != 0 {
		t.Errorf("a read was made before the refusal: typed %q, listed %q", *typed, *listed)
	}
}

// TestDiscoverEachStopsWhenTheOperatorStops: a cancelled context ends the list at once, with the cancellation
// as the cause, and no later endpoint is tried.
func TestDiscoverEachStopsWhenTheOperatorStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var asked []string
	old := discoverListed
	discoverListed = func(_ context.Context, _ []byte, _ string, endpoint string) (discover.Result, error) {
		asked = append(asked, endpoint)
		cancel()
		return discover.Result{}, context.Canceled
	}
	t.Cleanup(func() { discoverListed = old })
	_, _, err := discoverEach(ctx, listedConfig("10.0.0.1, 10.0.0.2"), "", "")
	if !errors.Is(err, context.Canceled) || !slices.Equal(asked, []string{"10.0.0.1"}) {
		t.Errorf("error %v, asked %q, want the cancellation and one try", err, asked)
	}
}

// TestDiscoverEachNamesTheEndpointThatAnswered: the printed commands carry the endpoint that gave the cluster,
// so a typed endpoint is named as typed, one listed endpoint needs no flag, and one of several is named.
func TestDiscoverEachNamesTheEndpointThatAnswered(t *testing.T) {
	eachFixture(t)
	for _, c := range []struct{ name, config, typed, want string }{
		{"typed", "10.0.0.1, 10.0.0.2", "10.9.9.9", "10.9.9.9"},
		{"one listed", "10.0.0.1", "", ""},
		{"several listed", "10.0.0.1, 10.0.0.2", "", "10.0.0.1"},
	} {
		_, answered, err := discoverEach(context.Background(), listedConfig(c.config), "", c.typed)
		if err != nil || answered != c.want {
			t.Errorf("%s: answered %q, %v, want %q", c.name, answered, err, c.want)
		}
	}
}
