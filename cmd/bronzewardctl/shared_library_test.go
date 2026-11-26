package main

import (
	"strings"
	"testing"
)

// The command line writes no shared (library) data, and shows what it is served of it: a record
// of no cluster, a shared draft entry beside a cluster entry of the same name, an adoption entry
// and an adoption removal, and a release's shared sources, which have no head revision, a removed
// adoption's among them.
func TestSharedLibraryIsShown(t *testing.T) {
	titleServer(t, func(a map[string]string) {
		a["GET /api/v1/fragments/frg_BWSYNTH"] = `{"id":"frg_BWSYNTH","cluster":null,"scope":"library","name":"ntp","layer":"cluster",` +
			`"revision":"frv_BWSYNTH","headRevision":3,"createdAt":"2026-11-19T10:00:00Z"}`
		a["GET /api/v1/releases/rel_BWSYNTH"] = strings.Replace(titleRel("drf_a"), `"sources":[]`,
			`"sources":[{"kind":"fragment","head":"frg_BWSYNTH","revision":"frv_BWSYNTH","headRevision":null},`+
				`{"kind":"profile","head":"prf_BWSYNTH","revision":"prv_BWSYNTH","headRevision":2},`+
				`{"kind":"fragment","head":"frg_BWDROPPED","revision":null,"headRevision":null}]`, 1)
		a["GET /api/v1/drafts/drf_a"] = `{"id":"drf_a","cluster":"cl_BWSYNTH","title":"` + titleDraft + `","state":"open","revision":3,"entries":[` +
			`{"kind":"fragment","name":"ntp","head":"frg_own","base":1,"revision":null},` +
			`{"kind":"fragment","name":"ntp","scope":"library","head":"frg_BWSYNTH","base":2,"adoptionBase":null,"revision":"frv_BWSYNTH"},` +
			`{"kind":"adoption","of":"fragment","name":"dns","head":"frg_BWDNS","base":null,"revision":"frv_BWDNS"},` +
			`{"kind":"adoption","of":"profile","name":"base-set","head":"prf_BWSET","base":4,"revision":null}]}`
	})
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"fragment", "show", "frg_BWSYNTH"}, []string{"scope library", "shared library"}},
		{[]string{"release", "show", "rel_BWSYNTH"}, []string{"frv_BWSYNTH\tshared library", "head revision 2",
			"frg_BWDROPPED - shared library: this cluster no longer follows it"}},
		{[]string{"draft", "show", "drf_a"}, []string{"removed", "shared library: changed (based on head revision 2)",
			"fragment dns frv_BWDNS shared library: this cluster adopts it (based on adoption none)",
			"profile base-set - shared library: this cluster stops following it (based on adoption 4)"}},
	} {
		code, out, errOut := runCtl(t, c.args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, err\n%s", c.args, code, errOut)
		}
		// A table's columns are padded with spaces.
		flat := strings.Join(strings.Fields(out), " ")
		for _, w := range c.want {
			if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("%v: no %q in\n%s", c.args, w, out)
			}
		}
	}
}
