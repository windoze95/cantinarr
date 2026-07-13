package remediation

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
)

func TestResolveTurnRejectsInheritedCodexUserOAuth(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	creds := newFakeCreds(t, database)
	if err := creds.SetAIConfig(credentials.AIProviderCodex, ""); err != nil {
		t.Fatalf("select Codex: %v", err)
	}

	factoryCalled := false
	runner := &Runner{
		creds: creds,
		newTurn: func(provider, apiKey, model string) (ai.TurnRunner, error) {
			factoryCalled = true
			return &scriptedTurn{}, nil
		},
	}

	turn, model, err := runner.resolveTurn(Settings{})
	if err == nil || !strings.Contains(err.Error(), "per-user OAuth") {
		t.Fatalf("resolveTurn error = %v, want clear per-user OAuth rejection", err)
	}
	if turn != nil {
		t.Fatalf("resolveTurn returned turn %#v for Codex", turn)
	}
	if model != "default" {
		t.Fatalf("resolved model = %q, want default", model)
	}
	if factoryCalled {
		t.Fatal("Codex reached the remediation turn factory")
	}
}

func TestResolveTurnKeepsAPIKeyProviderPath(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	creds := newFakeCreds(t, database)

	var gotProvider, gotAPIKey, gotModel string
	wantTurn := &scriptedTurn{}
	runner := &Runner{
		creds: creds,
		newTurn: func(provider, apiKey, model string) (ai.TurnRunner, error) {
			gotProvider, gotAPIKey, gotModel = provider, apiKey, model
			return wantTurn, nil
		},
	}

	turn, model, err := runner.resolveTurn(Settings{})
	if err != nil {
		t.Fatalf("resolveTurn: %v", err)
	}
	if turn != wantTurn {
		t.Fatalf("resolveTurn returned %#v, want fake turn", turn)
	}
	if gotProvider != credentials.AIProviderAnthropic || gotAPIKey != "fake-key" {
		t.Fatalf("factory provider/key = %q/%q", gotProvider, gotAPIKey)
	}
	wantModel := credentials.DefaultAIModel(credentials.AIProviderAnthropic)
	if gotModel != wantModel || model != wantModel {
		t.Fatalf("factory/returned model = %q/%q, want %q", gotModel, model, wantModel)
	}
}
