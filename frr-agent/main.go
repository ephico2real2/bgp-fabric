// frr-agent is a read-only HTTP front for FRR's vtysh. Nothing from the
// URL reaches the command line: only keys of a fixed allow-list are run.
// No auth — the fabric networks are the boundary (lab).
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// showCommands is the allow-list. A URL name is looked up here; the mapped
// string is the only thing passed to vtysh. Never interpolate the URL.
var showCommands = map[string]string{
	"bgp-summary":   "show bgp summary json",
	"bgp-ipv4":      "show bgp ipv4 unicast json",
	"bgp-neighbors": "show bgp neighbors json",
	"ip-route":      "show ip route json",
	"interface":     "show interface brief json",
}

func commandFor(name string) (string, bool) {
	cmd, ok := showCommands[name]
	return cmd, ok
}

type runnerFunc func(ctx context.Context, command string) (stdout, stderr string, err error)

func vtyshRun(ctx context.Context, command string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "vtysh", "-c", command)
	// After the deadline kills vtysh, Wait still blocks until every holder of
	// the stdout/stderr pipes exits. WaitDelay closes them so a child vtysh
	// left behind cannot pin the handler past the timeout.
	cmd.WaitDelay = time.Second
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	noStore(w)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	noStore(w)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func newMux(run runnerFunc, verbose bool) *http.ServeMux {
	mux := http.NewServeMux()
	logReq := func(r *http.Request) {
		if verbose {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		logReq(r)
		if r.Method != http.MethodGet {
			methodNotAllowed(w, r)
			return
		}
		healthz(w, r)
	})
	mux.HandleFunc("/show/{name}", func(w http.ResponseWriter, r *http.Request) {
		logReq(r)
		if r.Method != http.MethodGet {
			methodNotAllowed(w, r)
			return
		}
		handleShow(w, r, run)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logReq(r)
		noStore(w)
		http.Error(w, "not found", http.StatusNotFound)
	})
	return mux
}

func handleShow(w http.ResponseWriter, r *http.Request, run runnerFunc) {
	name := r.PathValue("name")
	cmd, ok := commandFor(name)
	if !ok {
		noStore(w)
		http.Error(w, "unknown show: "+name, http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	stdout, stderr, err := run(ctx, cmd)
	if err != nil {
		noStore(w)
		tail := strings.TrimSpace(stderr)
		if tail == "" {
			tail = err.Error()
		}
		if len(tail) > 512 {
			tail = tail[len(tail)-512:]
		}
		http.Error(w, tail, http.StatusBadGateway)
		return
	}
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(stdout))
}

func listenWithRetry(addr string, timeout time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(timeout)
	var last error
	var lastLog time.Time
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		last = err
		if time.Since(lastLog) >= 10*time.Second {
			log.Printf("frr-agent: bind %s failed: %v (retrying)", addr, err)
			lastLog = time.Now()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("frr-agent: could not bind %s after %s: %w (the management address must exist before the agent starts)", addr, timeout, last)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func main() {
	addr := os.Getenv("FRR_AGENT_ADDR")
	if addr == "" {
		log.Fatal("frr-agent: FRR_AGENT_ADDR is required (e.g. 10.200.200.11:8080)")
	}
	verbose := os.Getenv("FRR_AGENT_VERBOSE") == "1"
	ln, err := listenWithRetry(addr, 120*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("frr-agent: listening on %s", ln.Addr())
	srv := &http.Server{
		Handler:           newMux(vtyshRun, verbose),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
