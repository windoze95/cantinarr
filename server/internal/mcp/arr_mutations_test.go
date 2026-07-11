package mcp

import "testing"

func TestMutationHelpersReportNoDispatchAsPreflightFailure(t *testing.T) {
	tests := []struct {
		name string
		run  func() (string, error)
	}{
		{
			name: "grab missing guid",
			run: func() (string, error) {
				return GrabReleaseHelper(nil, nil, nil, "movie", "", 0, 0)
			},
		},
		{
			name: "search target unavailable",
			run: func() (string, error) {
				return TriggerSearchHelper(nil, nil, nil, nil, "movie", 42, nil, nil, 0, nil)
			},
		},
		{
			name: "rescan target unavailable",
			run: func() (string, error) {
				return RescanMediaHelper(nil, nil, nil, nil, "tv", 42, 0)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.run()
			if err == nil {
				t.Fatalf("no-op returned success %q", result)
			}
			classified, ok := err.(interface{ MutationNotStarted() bool })
			if !ok || !classified.MutationNotStarted() {
				t.Fatalf("no-op error is not a preflight failure: %T %v", err, err)
			}
		})
	}
}
