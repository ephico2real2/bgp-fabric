package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stubVtysh(t *testing.T, script string) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vtysh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// C4: a hung vtysh is killed at the context deadline and vtyshRun returns.
func TestVtyshRunHungIsKilledAtDeadline(t *testing.T) {
	stubVtysh(t, "#!/bin/sh\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := vtyshRun(ctx, "show bgp summary json")
	el := time.Since(start)
	if err == nil {
		t.Fatal("hung vtysh returned no error")
	}
	if el > 4*time.Second {
		t.Fatalf("vtyshRun took %s, deadline 3 s not enforced", el)
	}
	t.Logf("returned after %s: %v", el.Round(time.Millisecond), err)
}

// Not asked: a vtysh that leaves a child holding stdout (a pager, a hook)
// keeps cmd.Wait blocked after the kill because the pipe never closes.
func TestVtyshRunGrandchildHoldingStdout(t *testing.T) {
	stubVtysh(t, "#!/bin/sh\nsleep 20 &\nexec sleep 20\n")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := vtyshRun(ctx, "show bgp summary json")
	el := time.Since(start)
	if err == nil {
		t.Fatal("no error")
	}
	if el > 6*time.Second {
		t.Fatalf("vtyshRun took %s with a grandchild holding stdout; handler would hang", el)
	}
	t.Logf("returned after %s: %v", el.Round(time.Millisecond), err)
}
