package talos

import (
	"bufio"
	"os"
	"slices"
	"strings"
	"testing"

	cosiapi "github.com/cosi-project/runtime/api/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/api/cluster"
	"github.com/siderolabs/talos/pkg/machinery/api/inspect"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/api/security"
	"github.com/siderolabs/talos/pkg/machinery/api/storage"
	timeapi "github.com/siderolabs/talos/pkg/machinery/api/time"
	"google.golang.org/grpc"
)

// methodsFile is the classification scenarios/s1 counts mutations by.
const methodsFile = "../../fixtures/talos-api-methods.tsv"

// apiServices are every gRPC service of the Talos API in the pinned modules.
var apiServices = []*grpc.ServiceDesc{
	&cluster.ClusterService_ServiceDesc, &cosiapi.State_ServiceDesc, &inspect.InspectService_ServiceDesc,
	&machine.DebugService_ServiceDesc, &machine.ImageService_ServiceDesc, &machine.LifecycleService_ServiceDesc,
	&machine.MachineService_ServiceDesc, &security.SecurityService_ServiceDesc,
	&storage.StorageService_ServiceDesc, &timeapi.TimeService_ServiceDesc,
}

// deniedClients are the entries of denied that name a client, not a method.
var deniedClients = []string{
	"MachineClient", "ClusterClient", "StorageClient", "TimeClient", "InspectClient", "ImageClient",
	"DebugClient", "LifecycleClient", "Inspect",
}

// deniedWrappers are the entries of denied that are client functions over another method: the
// method they send.
var deniedWrappers = map[string]string{
	"ResetGeneric": "Reset", "UpgradeWithOptions": "Upgrade",
	"Modify": "Update", "StateModify": "Update", "StateModifyWithResult": "Update", "Teardown": "Update",
	"AddFinalizer": "Update", "RemoveFinalizer": "Update",
}

// apiMethods are the full method names of services, as apid logs them.
func apiMethods(services []*grpc.ServiceDesc) []string {
	var all []string
	for _, s := range services {
		for _, m := range s.Methods {
			all = append(all, s.ServiceName+"/"+m.MethodName)
		}
		for _, m := range s.Streams {
			all = append(all, s.ServiceName+"/"+m.StreamName)
		}
	}
	slices.Sort(all)
	return all
}

// readMethods reads the classification: method name to class.
func readMethods(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	class := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, c, ok := strings.Cut(line, "\t")
		if !ok || (c != "read" && c != "mutating") {
			t.Fatalf("%s: row %q is not <method> TAB read|mutating", path, line)
		}
		if _, dup := class[name]; dup {
			t.Fatalf("%s: %s is listed twice", path, name)
		}
		class[name] = c
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return class
}

// classificationProblems reports every method of services the classification does not list, every
// row that is no method of theirs, and every method the guard denies that is not mutating.
func classificationProblems(class map[string]string, services []*grpc.ServiceDesc, denied []string) []string {
	var problems []string
	methods := apiMethods(services)
	for _, m := range methods {
		if _, ok := class[m]; !ok {
			problems = append(problems, "unclassified: "+m)
		}
	}
	for m := range class {
		if !slices.Contains(methods, m) {
			problems = append(problems, "no such method: "+m)
		}
	}
	for _, d := range denied {
		if slices.Contains(deniedClients, d) {
			continue
		}
		name := d
		if w, ok := deniedWrappers[d]; ok {
			name = w
		}
		found := false
		for _, m := range methods {
			if _, short, _ := strings.Cut(m, "/"); short == name {
				found = true
				// An unclassified method is reported once, above.
				if c, ok := class[m]; ok && c != "mutating" {
					problems = append(problems, "denied by the guard but not mutating: "+m)
				}
			}
		}
		if !found {
			problems = append(problems, "denied by the guard, no method sends it: "+d)
		}
	}
	slices.Sort(problems)
	return problems
}

// TestTalosAPIMethods: the classification lists every method of the pinned Talos API services once,
// and nothing else, so a module bump that adds a method fails here until it is classified; and
// every method the read-only guard denies is mutating, so the two lists cannot drift apart.
func TestTalosAPIMethods(t *testing.T) {
	class := readMethods(t, methodsFile)
	if p := classificationProblems(class, apiServices, denied); len(p) > 0 {
		t.Fatalf("%s:\n%s", methodsFile, strings.Join(p, "\n"))
	}
	t.Logf("%d methods classified", len(class))

	// Controls, each on a copy: a method missing, a row for no method, a denied method read, a
	// service the file does not cover, and a denied name no method sends.
	without := func(drop string, set map[string]string) map[string]string {
		c := map[string]string{}
		for k, v := range class {
			if k != drop {
				c[k] = v
			}
		}
		for k, v := range set {
			c[k] = v
		}
		return c
	}
	extra := &grpc.ServiceDesc{ServiceName: "machine.NewService", Methods: []grpc.MethodDesc{{MethodName: "Version"}}}
	for name, c := range map[string]struct {
		class    map[string]string
		services []*grpc.ServiceDesc
		denied   []string
		want     string
	}{
		"missing":       {without("machine.MachineService/Reset", nil), apiServices, denied, "unclassified: machine.MachineService/Reset"},
		"stale":         {without("", map[string]string{"machine.MachineService/Gone": "read"}), apiServices, denied, "no such method: machine.MachineService/Gone"},
		"denied read":   {without("", map[string]string{"machine.MachineService/GenerateClientConfiguration": "read"}), apiServices, denied, "denied by the guard but not mutating: machine.MachineService/GenerateClientConfiguration"},
		"new service":   {class, append(slices.Clone(apiServices), extra), denied, "unclassified: machine.NewService/Version"},
		"denied unsent": {class, apiServices, append(slices.Clone(denied), "Vanish"), "denied by the guard, no method sends it: Vanish"},
	} {
		if p := classificationProblems(c.class, c.services, c.denied); !slices.Equal(p, []string{c.want}) {
			t.Errorf("control %s: got %q, want %q", name, p, c.want)
		}
	}
}
