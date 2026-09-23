package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The wiring, which nothing covered: ten of eleven mutants planted in it
// survived the whole suite — the route table, the element, the fetch handler,
// the stylesheet and the Containerfile's stage scoping. The pure functions
// were already covered; these are the parts that decide whether a reader ever
// sees a true number.

// A `-dirty` suffix is the difference between "this commit" and "this commit
// plus edits nobody committed". Dropping it is how a header comes to name a
// commit that does not contain the code being served.
func TestShortRevisionKeepsWhatFollowsTheSHA(t *testing.T) {
	saved := revision
	defer func() { revision = saved }()

	for _, tc := range []struct{ in, want string }{
		{"4cf18646880b540d48b029ea25a35ebe2e1821c2", "4cf1864"},
		{"4cf18646880b540d48b029ea25a35ebe2e1821c2-dirty", "4cf1864-dirty"},
		{"v1.2.3-4-g4cf1864-dirty", "v1.2.3-4-g4cf1864-dirty"},
		{"unknown", "unknown"},
		{"", ""},
	} {
		revision = tc.in
		if got := shortRevision(); got != tc.want {
			t.Errorf("shortRevision() with %q = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A handler that is correct but never registered serves 404. Every other test
// here calls the method directly, which cannot see that — deleting the
// mux.HandleFunc line passed the whole suite.
func TestTheVersionRouteIsRegistered(t *testing.T) {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		t.Fatalf("static: %v", err)
	}
	mux := routes(newHub(newPoller(nil, nil, time.Second), 10), static)

	for _, path := range []string{"/api/state", "/api/signal", "/api/events", "/api/version", "/healthz"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("GET %s is not routed (404) — the handler exists but nothing serves it", path)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s Cache-Control = %q, want no-store", path, got)
		}
	}
}

// The header only appears if the page is wired to it: an element with the id
// app.js looks up, a call that fills it, and the style that marks a build the
// pipeline did not make. Deleting any one of the three left every gate green.
func TestThePageIsWiredToShowTheBuild(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		return string(b)
	}

	if !strings.Contains(read("static/index.html"), `id="build"`) {
		t.Error(`static/index.html has no element with id="build", so nothing can show the build`)
	}
	appjs := read("static/app.js")
	if !strings.Contains(appjs, `fetch("/api/version"`) {
		t.Error("static/app.js never asks /api/version")
	}
	if !strings.Contains(appjs, "ui.applyBuildLabel(") {
		t.Error("static/app.js does not call ui.applyBuildLabel, so the element is never filled or unhidden")
	}
	css := read("static/app.css")
	for _, rule := range []string{`.build[data-unknown="yes"]`, `.build[data-dirty="yes"]`} {
		if !strings.Contains(css, rule) {
			t.Errorf("static/app.css has no %s rule: a build nobody can name looks like one that is fine", rule)
		}
	}
}

// ARG is per-stage. Without its own ARG the final stage expands $REVISION to
// the empty string and the label is silently blank while the binary is right.
// The first stage's ARG is not enough, so this checks the stage the LABEL is in.
func TestTheLabelStageDeclaresTheArguments(t *testing.T) {
	b, err := os.ReadFile("Containerfile")
	if err != nil {
		t.Fatalf("Containerfile: %v", err)
	}
	stages := regexp.MustCompile(`(?m)^FROM `).Split(string(b), -1)
	var labelStage string
	for _, s := range stages {
		if strings.Contains(s, "org.opencontainers.image.revision") {
			labelStage = s
		}
	}
	if labelStage == "" {
		t.Fatal("no stage carries the org.opencontainers.image.revision label")
	}
	for _, arg := range []string{"ARG REVISION", "ARG BUILT"} {
		if !strings.Contains(labelStage, arg) {
			t.Errorf("the stage that sets the labels does not declare %s, so the label expands to \"\"", arg)
		}
	}
	if !strings.Contains(string(b), "-X main.revision=${REVISION}") ||
		!strings.Contains(string(b), "-X main.built=${BUILT}") {
		t.Error("the linker flags do not name ${REVISION} and ${BUILT}, so the binary and the labels can disagree")
	}
}
