package auth

import "github.com/ginsys/bronzeward/internal/config"

// Denied is deployment configuration's deniedSubjects (persistence-api.md §10.4). It survives a
// database restore, which can lose a revocation row.
type Denied struct {
	humans   map[[2]string]bool
	services map[string]bool
}

func NewDenied(list []config.DeniedSubject) Denied {
	d := Denied{humans: map[[2]string]bool{}, services: map[string]bool{}}
	for _, e := range list {
		if e.Identity != "" {
			d.services[e.Identity] = true
		} else {
			d.humans[[2]string{e.Iss, e.Sub}] = true
		}
	}
	return d
}

func (d Denied) Human(iss, sub string) bool   { return d.humans[[2]string{iss, sub}] }
func (d Denied) Service(identity string) bool { return d.services[identity] }
