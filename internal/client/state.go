package client

import (
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v3"
)

// state is the CLI's non-secret state file (decision T11 of #256, client-contracts.md §8.3): the
// last-seen ETag per context and draft, kept under the context's name and then its server, so a
// context removed and added again for another server never shares, overwrites or drops the other
// server's entry. An entry names the epoch its ETags were seen at; at another epoch it reads as none.
type state struct {
	Contexts map[string]map[string]*serverState `yaml:"contexts,omitempty"`
}

// serverState's Started names the ingestions this client started at the entry's epoch: only after
// one of them succeeds does the client adopt the draft's re-read ETag (client-contracts.md §8.3).
type serverState struct {
	Epoch   string            `yaml:"epoch"`
	ETags   map[string]string `yaml:"etags,omitempty"`
	Started []string          `yaml:"started,omitempty"`
}

func statePath() (string, error) {
	dir, err := stateDir()
	return filepath.Join(dir, "state.yaml"), err
}

// kept is x's entry at its server and current epoch, nil when none is kept.
func kept(x *Context) (*serverState, error) {
	p, err := statePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var s state
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, x.fail(printableError(err))
	}
	if ss := s.Contexts[x.Name][x.Server]; ss != nil && ss.Epoch == x.LastEpoch {
		return ss, nil
	}
	return nil, nil
}

// keep changes x's entry at its server and current epoch. A change that adds replaces an entry kept
// at another epoch; one that only removes leaves that entry alone, because what it removes was never
// kept there, and another command may have just kept its own state at that epoch.
func keep(x *Context, adds bool, change func(*serverState)) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	return update(p, func(s *state) error {
		if ss := s.Contexts[x.Name][x.Server]; !adds && (ss == nil || ss.Epoch != x.LastEpoch) {
			return nil
		}
		if s.Contexts == nil {
			s.Contexts = map[string]map[string]*serverState{}
		}
		if s.Contexts[x.Name] == nil {
			s.Contexts[x.Name] = map[string]*serverState{}
		}
		ss := s.Contexts[x.Name][x.Server]
		if ss == nil || ss.Epoch != x.LastEpoch {
			ss = &serverState{Epoch: x.LastEpoch}
			s.Contexts[x.Name][x.Server] = ss
		}
		change(ss)
		return nil
	})
}

// ETag is the last-seen ETag of draft in x, "" when none is kept for x's server and epoch.
func ETag(x *Context, draft string) (string, error) {
	ss, err := kept(x)
	if ss == nil {
		return "", err
	}
	return ss.ETags[draft], nil
}

// KeepETag keeps etag as the last-seen ETag of draft in x; "" forgets it, as when the draft closes.
func KeepETag(x *Context, draft, etag string) error {
	return keep(x, etag != "", func(ss *serverState) {
		if ss.ETags == nil {
			// An entry whose last ETag was forgotten is written without the map and reads back without it.
			ss.ETags = map[string]string{}
		}
		if etag == "" {
			delete(ss.ETags, draft)
		} else {
			ss.ETags[draft] = etag
		}
	})
}

// Started reports whether this client started ingestion ing at x's server and current epoch.
func Started(x *Context, ing string) (bool, error) {
	ss, err := kept(x)
	return ss != nil && slices.Contains(ss.Started, ing), err
}

// KeepStarted records that this client started ingestion ing; with started false it forgets it, as
// when the ingestion ends. An epoch change drops the record with the entry's ETags.
func KeepStarted(x *Context, ing string, started bool) error {
	return keep(x, started, func(ss *serverState) {
		ss.Started = slices.DeleteFunc(ss.Started, func(s string) bool { return s == ing })
		if started {
			ss.Started = append(ss.Started, ing)
		}
	})
}

// DropState forgets what was kept for the context named name at server, when that context is
// removed.
func DropState(name, server string) error {
	return forget(name, server, func(*serverState) bool { return true })
}

// dropEarlier forgets what x kept at its server in an earlier epoch (§8.5 item 1), keeping an ETag
// this command kept at x's current epoch.
func dropEarlier(x *Context) error {
	return forget(x.Name, x.Server, func(ss *serverState) bool { return ss.Epoch != x.LastEpoch })
}

func forget(name, server string, stale func(*serverState) bool) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil
	}
	return update(p, func(s *state) error {
		if ss := s.Contexts[name][server]; ss != nil && stale(ss) {
			delete(s.Contexts[name], server)
			if len(s.Contexts[name]) == 0 {
				delete(s.Contexts, name)
			}
		}
		return nil
	})
}
