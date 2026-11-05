package clitest

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waiting returns once n processes are blocked on the flock of path, as /proc/locks lists them. A
// process blocked there has read the token file already, so no sleep decides the ordering.
func waiting(t *testing.T, path string, n int) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ino := ":" + strconv.FormatUint(fi.Sys().(*syscall.Stat_t).Ino, 10) + " "
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		b, err := os.ReadFile("/proc/locks")
		if err != nil {
			t.Fatal(err)
		}
		blocked := 0
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "-> FLOCK") && strings.Contains(l, ino) {
				blocked++
			}
		}
		if blocked >= n {
			return
		}
	}
	t.Fatalf("%d processes did not block on %s within 10 s", n, path)
}
