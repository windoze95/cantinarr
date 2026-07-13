package remediation

import (
	"context"
	"errors"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
)

func TestResolveTurnUsesOnlyServerOwnedSharedResolver(t *testing.T) {
	wantTurn := &scriptedTurn{}
	runner := &Runner{
		turns: autonomousTurnResolverFunc(func(context.Context) (ai.AutonomousTurn, error) {
			return ai.AutonomousTurn{Runner: wantTurn, Provider: "codex", Model: "shared-model"}, nil
		}),
	}

	// The resolver API has no provider or model argument: remediation cannot
	// switch or block the live shared selection.
	turn, model, err := runner.resolveTurn(context.Background())
	if err != nil {
		t.Fatalf("resolveTurn: %v", err)
	}
	if turn != wantTurn {
		t.Fatalf("resolveTurn returned %#v, want fake shared turn", turn)
	}
	if model != "shared-model" {
		t.Fatalf("returned model = %q, want shared-model", model)
	}
}

func TestResolveTurnFailsClosedWhenSharedResolverFails(t *testing.T) {
	wantErr := errors.New("shared account disconnected")
	runner := &Runner{
		turns: autonomousTurnResolverFunc(func(context.Context) (ai.AutonomousTurn, error) {
			return ai.AutonomousTurn{Provider: "codex", Model: "default"}, wantErr
		}),
	}

	turn, model, err := runner.resolveTurn(context.Background())
	if !errors.Is(err, wantErr) || turn != nil || model != "default" {
		t.Fatalf("resolveTurn = turn %#v model %q err %v", turn, model, err)
	}
}
