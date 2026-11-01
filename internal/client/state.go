package client

import (
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// state is the CLI's non-secret state file (decision T11 of #256, client-contracts.md §8.3): the
// last-seen ETag per context and draft. Each context's entry names the server and epoch its ETags
// were seen at, so one kept for another server or an earlier epoch reads as none.
type state struct {
	Contexts map[string]*contextState `yaml:"contexts,omitempty"`
}

type contextState struct {
	Server string            `yaml:"server"`
	Epoch  string            `yaml:"epoch"`
	ETags  map[string]string `yaml:"etags,omitempty"`
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
	if cs := s.Contexts[x.Name]; cs != nil && cs.Server == x.Server && cs.Epoch == x.LastEpoch {
		return cs.ETags[draft], nil
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
		cs := s.Contexts[x.Name]
		if cs == nil || cs.Server != x.Server || cs.Epoch != x.LastEpoch {
			cs = &contextState{Server: x.Server, Epoch: x.LastEpoch}
		}
		if cs.ETags == nil {
			cs.ETags = map[string]string{}
		}
		if s.Contexts == nil {
			s.Contexts = map[string]*contextState{}
		}
		s.Contexts[x.Name] = cs
		if etag == "" {
			delete(cs.ETags, draft)
		} else {
			cs.ETags[draft] = etag
		}
		return nil
	})
}

// DropState forgets everything kept for the context named name, when the context is removed.
func DropState(name string) error {
	return forget(name, func(*contextState) bool { return true })
}

// dropEarlier forgets what x kept for another server or an earlier epoch (§8.5 item 1), keeping an
// ETag this command kept at x's current epoch.
func dropEarlier(x *Context) error {
	return forget(x.Name, func(cs *contextState) bool { return cs.Server != x.Server || cs.Epoch != x.LastEpoch })
}

func forget(name string, stale func(*contextState) bool) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil
	}
	return update(p, func(s *state) error {
		if cs := s.Contexts[name]; cs != nil && stale(cs) {
			delete(s.Contexts, name)
		}
		return nil
	})
}
