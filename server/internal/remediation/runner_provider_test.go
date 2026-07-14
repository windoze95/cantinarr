package remediation

import (
	"context"
	"errors"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
)

func TestResolveTurnUsesOnlyServerOwnedSharedResolver(t *testing.T) {
	wantTurn := &scriptedTurn{}
	wantOverride := ai.AutonomousModelOverride{Provider: "codex", Model: "gpt-remediation"}
	runner := &Runner{
		turns: autonomousTurnResolverFunc(func(_ context.Context, override ai.AutonomousModelOverride) (ai.AutonomousTurn, error) {
			if override != wantOverride {
				t.Fatalf("override = %#v, want %#v", override, wantOverride)
			}
			return ai.AutonomousTurn{Runner: wantTurn, Provider: "codex", Model: "shared-model"}, nil
		}),
	}

	turn, model, err := runner.resolveTurn(context.Background(), Settings{
		ModelOverrideProvider: wantOverride.Provider,
		ModelOverride:         wantOverride.Model,
	})
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
		turns: autonomousTurnResolverFunc(func(context.Context, ai.AutonomousModelOverride) (ai.AutonomousTurn, error) {
			return ai.AutonomousTurn{Provider: "codex", Model: "default"}, wantErr
		}),
	}

	turn, model, err := runner.resolveTurn(context.Background(), Settings{})
	if !errors.Is(err, wantErr) || turn != nil || model != "default" {
		t.Fatalf("resolveTurn = turn %#v model %q err %v", turn, model, err)
	}
}
