package appletv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func scriptRunner(t *testing.T, script string) ProcessRunner {
	t.Helper()
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return ProcessRunner{Binary: path}
}

func TestProcessWorkerUsesPrivatePipesAndAnExplicitCommit(t *testing.T) {
	runner := scriptRunner(t, `
test "$#" -eq 0
IFS= read -r request
case "$request" in *synthetic-pairing-credential*) ;; *) exit 1 ;; esac
printf '%s\n' '{"state":"ready"}'
IFS= read -r commit
test "$commit" = '{"commit":true}'
printf '%s\n' '{"state":"sent"}'
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conversation, err := runner.Start(ctx, Command{Action: "open", Credentials: testCredential})
	if err != nil {
		t.Fatal(err)
	}
	defer conversation.Close()
	if reply, err := conversation.Receive(); err != nil || reply.State != "ready" {
		t.Fatalf("prepare: %+v %v", reply, err)
	}
	if err := conversation.Send(map[string]bool{"commit": true}); err != nil {
		t.Fatal(err)
	}
	if reply, err := conversation.Receive(); err != nil || reply.State != "sent" {
		t.Fatalf("commit: %+v %v", reply, err)
	}
}

func TestProcessWorkerHasABoundedLifetimeAndSanitizesFailures(t *testing.T) {
	runner := scriptRunner(t, "IFS= read -r request\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	conversation, err := runner.Start(ctx, Command{Action: "open"})
	if err != nil {
		t.Fatal(err)
	}
	defer conversation.Close()
	if _, err := conversation.Receive(); !errors.Is(err, problem("timeout")) {
		t.Fatalf("unbounded worker or wrong timeout: %v", err)
	}
	runner = scriptRunner(t, "IFS= read -r request\nprintf '%s\\n' '{\"state\":\"error\",\"code\":\"private-host credential-sentinel\"}'\n")
	conversation, err = runner.Start(context.Background(), Command{Action: "check"})
	if err != nil {
		t.Fatal(err)
	}
	defer conversation.Close()
	if _, err := conversation.Receive(); !errors.Is(err, problem("unavailable")) {
		t.Fatalf("unsafe helper error: %v", err)
	}
}
