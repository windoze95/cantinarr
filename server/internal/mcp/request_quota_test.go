package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	requestsvc "github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/requestquota"
)

func TestRESTAndMCPShareStructuredQuotaRefusals(t *testing.T) {
	database, e := db.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer database.Close()
	if _, e = database.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES (1,'requester','','user'),(2,'admin','','admin')`); e != nil {
		t.Fatal(e)
	}
	service := requestsvc.NewService(database, nil, nil, nil)
	if e = service.SetGlobalSettings(requestsvc.GlobalSettings{RequireApproval: true}); e != nil {
		t.Fatal(e)
	}
	one := 1
	if e = service.Quotas.Save(2, 0, []requestquota.Rule{{Key: requestquota.Key{MediaType: "movie"}, Count: &one, WindowDays: 7}}); e != nil {
		t.Fatal(e)
	}
	server := NewToolServer(nil, service, nil, nil)
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) { return auth.RoleUser, nil })
	caller := CallContext{UserID: 1, Role: auth.RoleUser, DeviceID: "test", Reauthorize: true}
	options, e := server.ExecuteTool(context.Background(), "get_request_options", json.RawMessage(`{"media_type":"movie","preview":{"tmdb_id":550,"title":"Movie"}}`), caller)
	if e != nil || !strings.Contains(options.Text, `"requested_units":1`) || !strings.Contains(options.Text, `"request_quotas"`) {
		t.Fatalf("options: %+v %v", options, e)
	}
	if _, e = server.ExecuteTool(context.Background(), "request_media", json.RawMessage(`{"tmdb_id":550,"media_type":"movie","title":"Movie"}`), caller); e != nil {
		t.Fatal(e)
	}
	result, e := server.ExecuteTool(context.Background(), "request_media", json.RawMessage(`{"tmdb_id":551,"media_type":"movie","title":"Next"}`), caller)
	if e != nil {
		t.Fatal(e)
	}
	var mcpFailure requestquota.Exceeded
	if e = json.Unmarshal([]byte(result.Text), &mcpFailure); e != nil || mcpFailure.Code != "request_quota_exceeded" || len(mcpFailure.Allowances) != 1 {
		t.Fatalf("structured failure lost: %s %v", result.Text, e)
	}
	r := httptest.NewRequest("POST", "/api/requests", strings.NewReader(`{"tmdb_id":551,"media_type":"movie","title":"Next"}`))
	r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: auth.RoleUser}))
	w := httptest.NewRecorder()
	requestsvc.NewHandler(service).Create(w, r)
	var restFailure requestquota.Exceeded
	if w.Code != 429 || json.Unmarshal(w.Body.Bytes(), &restFailure) != nil || restFailure.Code != mcpFailure.Code || restFailure.Allowances[0].RequestedUnits != mcpFailure.Allowances[0].RequestedUnits || !restFailure.EarliestFitsAt.Equal(*mcpFailure.EarliestFitsAt) {
		t.Fatalf("REST and MCP differ: %s / %s", w.Body.String(), result.Text)
	}
	var count int
	database.QueryRow(`SELECT COUNT(*) FROM request_log`).Scan(&count)
	if count != 1 {
		t.Fatal("refusal saved work")
	}
	props := findToolDefinition("request_media").InputSchema["properties"].(map[string]interface{})
	if props["seasons"] == nil || props["season_scope"] == nil {
		t.Fatal("assistant cannot reduce TV selection")
	}
}
