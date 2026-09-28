package request

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

func TestDeliveryLogsSavedProblemsWithoutLeakingUpstreamErrors(t *testing.T) {
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	for _, tc := range []struct {
		state, code string
		cause       error
	}{
		{"attention", "access_unavailable", nil},
		{"waiting_library", "author_import", nil},
		{"retry", "service_unavailable", &transporterr.Upstream{Message: "http://private-host/?apikey=secret-body", Status: 503, Transient: true}},
	} {
		t.Run(tc.code, func(t *testing.T) {
			s, uid := newChaptarrBookTestService(t, "http://unused")
			out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", Title: "Saved", ForeignID: "hc:100", BookFormat: "ebook"})
			if err != nil {
				t.Fatal(err)
			}
			token, _, ok := s.claimDelivery(out.RequestID, "ebook")
			if !ok {
				t.Fatal("could not claim delivery")
			}
			logs.Reset()
			s.finishDelivery(out.RequestID, "ebook", token, tc.state, tc.code, tc.cause)
			got := logs.String()
			if !strings.Contains(got, "state="+tc.state) || !strings.Contains(got, "code="+tc.code) || !strings.Contains(got, "attempt=1") {
				t.Fatalf("saved problem was not logged: %q", got)
			}
			if strings.Contains(got, "private-host") || strings.Contains(got, "secret-body") || strings.Contains(got, "apikey") {
				t.Fatalf("upstream text leaked: %q", got)
			}
			if tc.state == "retry" && !strings.Contains(got, "upstream_status=503") {
				t.Fatalf("lost upstream status: %q", got)
			}
			logs.Reset()
			s.finishDelivery(out.RequestID, "ebook", token, tc.state, tc.code, tc.cause)
			if logs.Len() != 0 {
				t.Fatal("expired lease emitted an outcome it did not save")
			}
		})
	}
}
