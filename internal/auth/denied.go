package auth

import "github.com/ginsys/bronzeward/internal/config"

// Denied is deployment configuration's deniedSubjects (persistence-api.md §10.4). It survives a
// database restore, which can lose a revocation row and, for a service identity created after
// the backup, the row that held its name.
type Denied struct {
	humans   map[[2]string]bool
	services map[string]bool
	entries  map[[2]string]bool // idn identifier and name, as one entry lists them
	names    map[string]bool
}

func NewDenied(list []config.DeniedSubject) Denied {
	d := Denied{humans: map[[2]string]bool{}, services: map[string]bool{}, entries: map[[2]string]bool{}, names: map[string]bool{}}
	for _, e := range list {
		if e.Identity != "" {
			d.services[e.Identity] = true
			d.entries[[2]string{e.Identity, e.Name}] = true
			if e.Name != "" { // config requires it; an empty one must not match every lookup without a name
				d.names[e.Name] = true
			}
		} else {
			d.humans[[2]string{e.Iss, e.Sub}] = true
		}
	}
	return d
}

func (d Denied) Human(iss, sub string) bool { return d.humans[[2]string{iss, sub}] }

// Service reports whether an entry lists the service identity or its name: a new identity created
// under a listed name, after a restore freed it, is refused as the listed one is.
func (d Denied) Service(identity, name string) bool {
	return d.services[identity] || d.names[name]
}

// ServiceEntry reports whether one entry lists both the service identity and its name, as §10.4
// has the operator do before revoking it, whatever other entries name that identifier.
func (d Denied) ServiceEntry(identity, name string) bool {
	return d.entries[[2]string{identity, name}]
}
