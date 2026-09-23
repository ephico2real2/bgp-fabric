package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The dashboard is asked "is this the build with the fix?" and, before this,
// the only way to answer was to fetch its assets and diff them against a
// checkout. These cover what it now answers with — including the case where
// it does not know, which has to say so rather than invent a number.

func TestShortRevisionTakesSevenCharacters(t *testing.T) {
	saved := revision
	defer func() { revision = saved }()

	revision = "4cf18646880b540d48b029ea25a35ebe2e1821c2"
	if got := shortRevision(); got != "4cf1864" {
		t.Fatalf("shortRevision() = %q, want 4cf1864", got)
	}
}

func TestShortRevisionDoesNotSliceTheWordUnknown(t *testing.T) {
	saved := revision
	defer func() { revision = saved }()

	// "unknown" is exactly seven characters, so a bare [:7] would return it
	// unchanged by accident. Anything SHORTER must also survive: a tag, or a
	// `git describe` of a shallow tree, is not a 40-character sha.
	for _, in := range []string{"unknown", "abc", "", "v1.2"} {
		revision = in
		if got := shortRevision(); got != in {
			t.Errorf("shortRevision() with %q = %q, want it unchanged", in, got)
		}
	}
}

func TestVersionEndpointReportsWhatWasLinkedIn(t *testing.T) {
	savedRev, savedBuilt := revision, built
	defer func() { revision, built = savedRev, savedBuilt }()
	revision = "4cf18646880b540d48b029ea25a35ebe2e1821c2"
	built = "2026-09-23T10:32:58Z"

	h := &hub{}
	rec := httptest.NewRecorder()
	h.apiVersion(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: a cached answer would name the build a reader is NOT looking at", cc)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body.String())
	}
	want := map[string]string{
		"revision": "4cf18646880b540d48b029ea25a35ebe2e1821c2",
		"short":    "4cf1864",
		"built":    "2026-09-23T10:32:58Z",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestAnUnlinkedBinarySaysUnknownRatherThanEmpty(t *testing.T) {
	savedRev, savedBuilt := revision, built
	defer func() { revision, built = savedRev, savedBuilt }()
	revision, built = "unknown", "unknown"

	h := &hub{}
	rec := httptest.NewRecorder()
	h.apiVersion(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	// An empty string would render as "build " with nothing after it; the page
	// distinguishes "unknown" and says "local build".
	for _, k := range []string{"revision", "short", "built"} {
		if got[k] != "unknown" {
			t.Errorf("%s = %q, want \"unknown\"", k, got[k])
		}
	}
}

// The values are only ever right if the build passes them. This reads the
// Containerfile rather than trusting that it does.
func TestTheContainerfileLinksTheRevisionIn(t *testing.T) {
	b, err := os.ReadFile("Containerfile")
	if err != nil {
		t.Fatalf("Containerfile: %v", err)
	}
	src := string(b)

	for _, arg := range []string{"ARG REVISION", "ARG BUILT"} {
		if !strings.Contains(src, arg) {
			t.Errorf("the build takes no %s argument", arg)
		}
	}
	for _, flag := range []string{"-X main.revision=", "-X main.built="} {
		if !strings.Contains(src, flag) {
			t.Errorf("%s is not linked in, so the binary cannot name its build", flag)
		}
	}
	// The same argument becomes the image label: `docker inspect` and the page
	// then cannot disagree about which build is running.
	label := regexp.MustCompile(`org\.opencontainers\.image\.revision="?\$REVISION`)
	if !label.MatchString(src) {
		t.Error("the OCI revision label is not built from the same argument")
	}
}

// Every JSON endpoint, not just the new one. A cached answer is a stale
// answer presented as current — the page's whole claim is that what it shows
// is what the fabric just said.
func TestEveryJSONEndpointRefusesToBeCached(t *testing.T) {
	h := newHub(newPoller(nil, nil, time.Second), 10)
	for _, tc := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"/api/state", h.apiState},
		{"/api/signal", h.apiSignal},
		{"/api/events", h.apiEvents},
		{"/api/version", h.apiVersion},
		// healthz too: a cached health check lets a proxy go on reporting a
		// dead dashboard as alive, which is the one answer that must always
		// be measured fresh.
		{"/healthz", h.healthz},
	} {
		rec := httptest.NewRecorder()
		tc.handler(rec, httptest.NewRequest(http.MethodGet, tc.name, nil))
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", tc.name, got)
		}
	}
}
