package api

import (
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// baseWith is a generated configuration whose HostnameConfig and KubeNodeConfig documents are those
// given, either left out when empty: the explicit hostname and the registerWithFQDN value of a machine
// whose import base it is.
func baseWith(t *testing.T, hostname string, fqdn *bool) string {
	t.Helper()
	var docs []string
	for _, d := range strings.Split(generatedConfig(t, constants.DefaultKubernetesVersion), "\n---\n") {
		if !strings.Contains(d, "kind: HostnameConfig") && !strings.Contains(d, "kind: KubeNodeConfig") {
			docs = append(docs, d)
		}
	}
	if hostname != "" {
		docs = append(docs, "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: "+hostname+"\n")
	}
	if fqdn != nil {
		v := "false"
		if *fqdn {
			v = "true"
		}
		docs = append(docs, "apiVersion: v1alpha1\nkind: KubeNodeConfig\nregisterWithFQDN: "+v+"\n")
	}
	return strings.Join(docs, "\n---\n")
}

func flag(v bool) *bool { return &v }

// A release refuses two machines that would register one Kubernetes node name where a hostname is
// filled from a name, `422 validation-failed` with the rule `hostname` (compilation.md §6.1 item 6,
// persistence-api.md §9.2). The names a.x.org and a.y.org give the node name a unless registerWithFQDN
// is true; a filled hostname also clashes with another machine's explicit one. A machine with no
// hostname document, an unnamed one and two explicit hostnames that were already equal are not refused.
func TestBuildReleaseHostnameClash(t *testing.T) {
	type machine struct {
		name     string
		hostname string // an explicit hostname in the import base, "" for the generated base's own document
		fqdn     *bool
	}
	for _, c := range []struct {
		label         string
		first, second machine
		refused       bool
		detail        []string // the refusal's text, in either order of the two machines; %S is the second machine's identifier
	}{
		{"names sharing a first label, registerWithFQDN unset", machine{name: "a.x.org"}, machine{name: "a.y.org"}, true,
			[]string{"Machine a.x.org and machine a.y.org would both register the Kubernetes node name a. Rename one of them, or set `registerWithFQDN` on both.",
				"Machine a.y.org and machine a.x.org would both register the Kubernetes node name a. Rename one of them, or set `registerWithFQDN` on both."}},
		{"registerWithFQDN true on both", machine{name: "a.x.org", fqdn: flag(true)}, machine{name: "a.y.org", fqdn: flag(true)}, false, nil},
		{"registerWithFQDN true on one", machine{name: "a.x.org", fqdn: flag(true)}, machine{name: "a.y.org"}, false, nil},
		{"filled against an explicit hostname of an unnamed machine", machine{name: "web-1"}, machine{hostname: "web-1.example.org"}, true,
			[]string{"Machine web-1 would take the hostname web-1 from its name, but the hostname configured on machine %S gives the same Kubernetes node name. " +
				"Rename machine web-1, or change the hostname in the configuration of machine %S."}},
		{"filled against an explicit hostname of a named machine", machine{name: "web-1"}, machine{name: "db-1", hostname: "WEB_1"}, true,
			[]string{"Machine web-1 would take the hostname web-1 from its name, but the hostname configured on machine db-1 gives the same Kubernetes node name. " +
				"Rename machine web-1, or change the hostname in the configuration of machine db-1."}},
		// The strings of ginsys/bronzeward#1040: the node name is derived by Talos's rules, so a case,
		// an underscore and a dropped character do not hide a clash.
		{"filled against an explicit hostname in capitals, with an underscore and a domain", machine{name: "node-a"}, machine{hostname: "NODE_A.example.org"}, true,
			[]string{"Machine node-a would take the hostname node-a from its name, but the hostname configured on machine %S gives the same Kubernetes node name. " +
				"Rename machine node-a, or change the hostname in the configuration of machine %S."}},
		{"filled against an explicit hostname with underscores at both ends", machine{name: "node-a"}, machine{hostname: "_node-a_"}, true,
			[]string{"Machine node-a would take the hostname node-a from its name, but the hostname configured on machine %S gives the same Kubernetes node name. " +
				"Rename machine node-a, or change the hostname in the configuration of machine %S."}},
		{"filled against an explicit hostname with a character the node name drops", machine{name: "node-a"}, machine{hostname: "node!-a"}, true,
			[]string{"Machine node-a would take the hostname node-a from its name, but the hostname configured on machine %S gives the same Kubernetes node name. " +
				"Rename machine node-a, or change the hostname in the configuration of machine %S."}},
		{"two explicit hostnames already equal", machine{hostname: "same"}, machine{hostname: "same"}, false, nil},
		{"an unnamed machine against an explicit hostname", machine{}, machine{hostname: "web-1"}, false, nil},
		{"filled against another name", machine{name: "web-1"}, machine{name: "web-2"}, false, nil},
	} {
		t.Run(c.label, func(t *testing.T) {
			b := newBuildEnv(t)
			second := b.secondMachine(constants.DefaultKubernetesVersion)
			for _, m := range []struct {
				id string
				m  machine
			}{{b.machine, c.first}, {second, c.second}} {
				if m.m.hostname != "" || m.m.fqdn != nil {
					b.importBase(m.id, baseWith(t, m.m.hostname, m.m.fqdn))
				}
				mustExec(t, b.db, `UPDATE machine SET name = NULLIF($2, '') WHERE id = $1`, m.id, m.m.name)
			}
			_, ref := b.build()
			if !c.refused {
				if ref != nil {
					t.Fatalf("refused: %v %v", ref, ref.extra)
				}
				return
			}
			if ref == nil || ref.status != 422 || ref.code != "validation-failed" || ref.extra["rule"] != "hostname" {
				t.Fatalf("got %+v, want 422 validation-failed with the rule hostname", ref)
			}
			ok := false
			for _, d := range c.detail {
				ok = ok || ref.detail == strings.ReplaceAll(d, "%S", second)
			}
			if !ok {
				t.Errorf("detail %q, want one of %q", ref.detail, c.detail)
			}
			b.noSecretIn(ref.detail)
		})
	}
}
