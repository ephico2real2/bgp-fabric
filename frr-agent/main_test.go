package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCommandForAllowList(t *testing.T) {
	want := map[string]string{
		"bgp-summary":   "show bgp summary json",
		"bgp-ipv4":      "show bgp ipv4 unicast json",
		"bgp-neighbors": "show bgp neighbors json",
		"ip-route":      "show ip route json",
		"interface":     "show interface brief json",
	}
	for name, cmd := range want {
		got, ok := commandFor(name)
		if !ok {
			t.Fatalf("commandFor(%q) missing", name)
		}
		if got != cmd {
			t.Fatalf("commandFor(%q)=%q, want %q", name, got, cmd)
		}
	}
}

func TestCommandForRejects(t *testing.T) {
	names := []string{"..", "bgp-summary;reboot", "", "reboot", "bgp-summary json", "show"}
	for _, name := range names {
		if cmd, ok := commandFor(name); ok {
			t.Fatalf("commandFor(%q) accepted as %q", name, cmd)
		}
	}
}

func testMux() *http.ServeMux {
	return newMux(func(_ context.Context, command string) (string, string, error) {
		return `{"ok":true,"command":"` + command + `"}`, "", nil
	}, false)
}

func TestShowUnknownIs404(t *testing.T) {
	srv := httptest.NewServer(testMux())
	t.Cleanup(srv.Close)
	for _, path := range []string{
		"/show/..",
		"/show/bgp-summary;reboot",
		"/show/",
		"/show/reboot",
		"/nope",
	} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status %d body %q, want 404", path, res.StatusCode, body)
		}
		if strings.Contains(path, "reboot") && !strings.Contains(string(body), "unknown show:") {
			t.Fatalf("%s: body %q, want unknown show:", path, body)
		}
	}
}

func TestPOSTIs405(t *testing.T) {
	srv := httptest.NewServer(testMux())
	t.Cleanup(srv.Close)
	for _, path := range []string{"/healthz", "/show/bgp-summary"} {
		res, err := http.Post(srv.URL+path, "text/plain", strings.NewReader("x"))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s: status %d, want 405", path, res.StatusCode)
		}
	}
}

func TestShowPassesRunnerJSON(t *testing.T) {
	const payload = `{"ipv4Unicast":{"routerId":"10.200.255.11","as":65101}}`
	mux := newMux(func(_ context.Context, command string) (string, string, error) {
		if command != "show bgp summary json" {
			t.Fatalf("runner got %q", command)
		}
		return payload, "", nil
	}, false)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/show/bgp-summary")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %q", res.StatusCode, body)
	}
	if string(body) != payload {
		t.Fatalf("body %q, want verbatim payload", body)
	}
	if res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type %q", res.Header.Get("Content-Type"))
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control %q", res.Header.Get("Cache-Control"))
	}
}

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(testMux())
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("healthz %d %q", res.StatusCode, body)
	}
}
