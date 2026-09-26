package discordnotify

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func decodeBody(w http.ResponseWriter, r *http.Request, body any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if err := d.Decode(body); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("unexpected trailing data")
	}
	return nil
}

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
	var body Update
	if r.Method != http.MethodDelete {
		if err := decodeBody(w, r, &body); err != nil {
			reply(w, 400, map[string]string{"error": "Invalid Discord settings."})
			return
		}
	}
	body.Webhook = strings.TrimSpace(body.Webhook)
	if strings.HasSuffix(r.URL.Path, "/test") {
		out, err := s.TestUpdate(r.Context(), body)
		if err != nil {
			reply(w, 400, map[string]string{"error": "The webhook could not be tested. Enter a valid Discord HTTPS webhook URL, or check the saved settings."})
			return
		}
		reply(w, 200, out)
		return
	}
	if err := s.SaveUpdate(body, r.Method == http.MethodDelete); err != nil {
		reply(w, 400, map[string]string{"error": "The Discord settings could not be saved. Check the webhook, numeric Discord IDs, display name, HTTPS avatar URL, and server storage."})
		return
	}
	out, err := s.Get()
	if err != nil {
		reply(w, 500, map[string]string{"error": "Settings saved, but delivery status could not be read."})
		return
	}
	reply(w, 200, out)
}
