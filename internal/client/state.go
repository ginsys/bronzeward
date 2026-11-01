package client

import (
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// state is the CLI's non-secret state file (decision T11 of #256, client-contracts.md §8.3): the
// last-seen ETag per context and draft, kept under the context's name and then its server, so a
// context removed and added again for another server never shares, overwrites or drops the other
// server's entry. An entry names the epoch its ETags were seen at; at another epoch it reads as none.
type state struct {
	Contexts map[string]map[string]*serverState `yaml:"contexts,omitempty"`
}

type serverState struct {
	Epoch string            `yaml:"epoch"`
	ETags map[string]string `yaml:"etags,omitempty"`
}

func statePath() (string, error) {
	dir, err := stateDir()
	return filepath.Join(dir, "state.yaml"), err
}

// ETag is the last-seen ETag of draft in x, "" when none is kept for x's server and epoch.
func ETag(x *Context, draft string) (string, error) {
	p, err := statePath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	var s state
	if err := yaml.Unmarshal(b, &s); err != nil {
		return "", x.fail(err)
	}
	if ss := s.Contexts[x.Name][x.Server]; ss != nil && ss.Epoch == x.LastEpoch {
		return ss.ETags[draft], nil
	}
	return "", nil
}

// KeepETag keeps etag as the last-seen ETag of draft in x; "" forgets it, as when the draft closes.
func KeepETag(x *Context, draft, etag string) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	return update(p, func(s *state) error {
		if s.Contexts == nil {
			s.Contexts = map[string]map[string]*serverState{}
		}
		if s.Contexts[x.Name] == nil {
			s.Contexts[x.Name] = map[string]*serverState{}
		}
		ss := s.Contexts[x.Name][x.Server]
		if ss == nil || ss.Epoch != x.LastEpoch {
			ss = &serverState{Epoch: x.LastEpoch, ETags: map[string]string{}}
			s.Contexts[x.Name][x.Server] = ss
		}
		if ss.ETags == nil {
			// An entry whose last ETag was forgotten is written without the map and reads back without it.
			ss.ETags = map[string]string{}
		}
		if etag == "" {
			delete(ss.ETags, draft)
		} else {
			ss.ETags[draft] = etag
		}
		return nil
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
