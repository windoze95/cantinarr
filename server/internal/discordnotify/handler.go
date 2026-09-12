package discordnotify

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Handler is mounted behind the admin credentials permission in the router.
func (s *Service) Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		out, err := s.Get()
		if err != nil {
			reply(w, 500, map[string]string{"error": "Discord settings and delivery status could not be read."})
			return
		}
		reply(w, 200, out)
		return
	}
	var body struct {
		IncludeAutoApproved *bool  `json:"include_auto_approved"`
		Enabled             bool   `json:"enabled"`
		Webhook             string `json:"webhook_url"`
	}
	if r.Method != http.MethodDelete {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		d.DisallowUnknownFields()
		if err := d.Decode(&body); err != nil {
			reply(w, 400, map[string]string{"error": "Invalid Discord settings."})
			return
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			reply(w, 400, map[string]string{"error": "Invalid Discord settings."})
			return
		}
	}
	body.Webhook = strings.TrimSpace(body.Webhook)
	if strings.HasSuffix(r.URL.Path, "/test") {
		out, err := s.Test(r.Context(), body.Webhook)
		if err != nil {
			reply(w, 400, map[string]string{"error": "The webhook could not be tested. Enter a valid Discord HTTPS webhook URL, or check the saved settings."})
			return
		}
		reply(w, 200, out)
		return
	}
	if err := s.Save(body.Enabled, body.Webhook, r.Method == http.MethodDelete, body.IncludeAutoApproved); err != nil {
		reply(w, 400, map[string]string{"error": "The Discord settings could not be saved. Check the webhook URL and server storage."})
		return
	}
	out, err := s.Get()
	if err != nil {
		reply(w, 500, map[string]string{"error": "Settings saved, but delivery status could not be read."})
		return
	}
	reply(w, 200, out)
}
