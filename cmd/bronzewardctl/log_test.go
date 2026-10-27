package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHardenSilencesTheLogger starts this test binary again: after harden, the bytes a server sends
// on an idle connection, which net/http quotes to the standard logger, are not printed. Control:
// before harden, net/http logs them.
func TestHardenSilencesTheLogger(t *testing.T) {
	if os.Getenv("BW_TEST_LOGGER") != "" {
		if os.Getenv("GOTRACEBACK") != "none" {
			var b lockedBuffer
			log.SetOutput(&b)
			unsolicited(t, "BWSYNTH-control")
			for end := time.Now().Add(5 * time.Second); !strings.Contains(b.String(), "BWSYNTH-control") && time.Now().Before(end); {
				time.Sleep(10 * time.Millisecond)
			}
			fmt.Fprintf(os.Stderr, "control logged: %t\n", strings.Contains(b.String(), "BWSYNTH-control"))
			log.SetOutput(os.Stderr)
		}
		if err := harden(); err != nil {
			t.Fatal(err)
		}
		unsolicited(t, "BWSYNTH-after")
		time.Sleep(time.Second)
		fmt.Fprintln(os.Stderr, "BWSYNTH-done")
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHardenSilencesTheLogger$")
	cmd.Env = append(os.Environ(), "BW_TEST_LOGGER=1", "GOTRACEBACK=all")
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if !strings.Contains(s, "control logged: true") || !strings.Contains(s, "BWSYNTH-done") || strings.Contains(s, "BWSYNTH-after") {
		t.Errorf("hardened process printed:\n%s", s)
	}
}

// unsolicited gets one answer from a server that sends extra bytes after it, on a connection the
// client keeps.
func unsolicited(t *testing.T, extra string) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReader(c)
		for h, err := br.ReadString('\n'); err == nil && h != "\r\n"; h, err = br.ReadString('\n') {
		}
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"+extra)
		time.Sleep(2 * time.Second)
		c.Close()
	}()
	r, err := http.Get("http://" + l.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
