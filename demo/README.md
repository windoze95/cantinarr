# Cantinarr Demo Server

A self-contained mock backend for App Store distribution review and product demos. It simulates the full current Cantinarr API with in-memory data — no external services, arr instances, or databases required. All seeded content is public-domain works or fiction invented for the demo.

## Running

```bash
cd demo
go run .
```

The server starts on **port 8484**.

| Env var | Default | Purpose |
|---------|---------|---------|
| `DEMO_SERVER_URL` | `http://localhost:<port>` | Advertised base URL, embedded in generated `cantinarr://connect` links |
| `DEMO_PORT` | `8484` | Listen port override for running several local copies side by side (the deployed demo never sets it) |

## Demo Credentials

| Username | Password | Role  |
|----------|----------|-------|
| admin    | demo     | admin |
| user     | demo     | user  |

There is **no registration endpoint and no invite code** — accounts come from the seeds above or from admin-generated connect links (a third seeded account, `riley`, has a pending invite to demo that flow).

A **well-known multi-use connect token** is baked in for reviewers, bound to the `user` account and exempt from single-use redemption:

```
cantinarr://connect?token=demo0000000000000000000000000000000000000000000000000000connect1&server=<urlencoded DEMO_SERVER_URL>
```

## What's Simulated

Full parity with the current Cantinarr API surface:

- **Auth & session** — JWT login, opaque `cnr1.` refresh tokens, connect-link redemption, per-user permissions, device management
- **Discovery** — trending, popular, top-rated, upcoming, and now-playing rows for movies, Airing This Week / Top Rated / Coming Soon for TV, real paging, full-text search, browse by genre, year range, rating, language, streaming service (per region), keyword, and studio, movie/TV/person details with cast, crew, studios, countries, budget and revenue, release dates by region, and IMDb/TMDB/Trakt link ids, Trakt lists/anticipated/calendar
- **Requests** — movie, TV (per-season), book (ebook/audiobook), and album requests with the full approval flow: pending → approve/deny → simulated download progress → available/partial, routed to a chosen library
- **Kids accounts** — one seeded requester is a kids account with a content policy (rating caps, unrated and genre blocks); every title surface hides what it may not see, a hidden title's page reads "not available to your account", and its requests are refused; admins see the Child tag on the Users screen and edit the policy from that user's request settings, with the real certification catalog (US and GB)
- **Multiple libraries** — two Radarr and two Sonarr instances, reached through additive per-user grants, so the Library chooser, the per-library status chips, and instance-scoped quality profiles all have something to show
- **Books browsing** — the Chaptarr library by author and by series, with per-format ownership on every title, and book pages that link out to Goodreads and Open Library (the classics carry their real Goodreads and Open Library work ids; the invented titles carry none and show no Links line)
- **Music browsing** — the Lidarr library by artist, Recently Added, owned-aware search, requestable albums that walk pending → requested → downloading → available, and the admin Music module (library, queue with the Import Doctor, wanted, calendar, history, release search)
- **Media-server access** — Jellyfin, Emby, and Plex as instances: create your own account, link one you already have, sign in with Plex, ask where a title can be watched; admins tag the Users screen, link/unlink, and import a server's existing accounts
- **AI chat** — streaming SSE assistant with tool calls and media results (kid-safe for the kids account), plus AI settings/credentials surfaces, the 41-tool registry, and the Codex and xAI Grok OAuth device flows
- **Admin console** — instance management (arrs incl. Lidarr, download clients incl. a qBittorrent in API-key mode, Tautulli and Tracearr, media servers), per-user library grants, arr library browsing and editing (fake Radarr/Sonarr/Chaptarr/Lidarr proxies; Radarr Edit Movie, tags, refresh), download-client queue/history for two clients, Monitoring with both providers, issues + AI remediation (agent actions, runs, approval rules, music included), configuration change history, the external address invite links are built from, users/devices, the 14-item setup checklist with skippable items, and update status
- **Live updates** — WebSocket hub pushing download progress, request status changes, queue snapshots, approvals, issues, agent actions, and Plex invite events to the right audiences

## File Map

| File | Owns |
|------|------|
| `main.go` | Env/flags (`DEMO_SERVER_URL`, `DEMO_PORT`), middleware (auth/CORS), router assembly, landing page |
| `types.go` | Shared enums/constants (statuses, media/service types, WS event names), JSON helpers |
| `state.go` | In-memory store: users, devices, tokens, instances, sessions; seeds and accessors |
| `ws.go` | WebSocket hub: auth, broadcast/admin/user scopes, envelope, pings |
| `auth.go` | `/api/auth/*` — status, login, refresh, connect, me, logout, password, plex-email, passkey stubs |
| `config.go` | `/api/config` (per-user visibility), `/api/admin/setup-status`, `/api/admin/update-status` |
| `users_admin.go` | Admin users, devices, connect tokens, test-push, per-user default instances and access grants |
| `contentpolicy.go` | Kids accounts: `/api/admin/certifications`, per-user content policies, the decision helpers every title chokepoint calls |
| `data_policy.go` | Per-title certifications (US/GB) for the catalog and the seeded kids policy |
| `external_address.go` | `/api/admin/external-address` — the origin invite links are built from |
| `mediaaccess.go` | `/api/media-servers*` + `/api/admin/media-servers*` — accounts, linking, Plex sign-in, watch links, import |
| `plex.go` | The simulated plex.tv PIN flow, shared by the instance editor and the user sign-in |
| `instances.go` | Instance CRUD, media roots, per-instance users and grants, media-server libraries, Plex link, test, proxy dispatch |
| `requests.go` | `/api/requests*` — create, options, TMDB status, lifecycle machine |
| `requests_admin.go` | Admin request queue, approve/deny, global request settings |
| `books.go` | Book status/library/recent/authors/series + the book request lifecycle |
| `music.go` | `/api/requests/music-*` (status, library, recent, artists, artist) + the music request lifecycle |
| `discover.go` | `/api/discover/*` (paged rows, browse filters, TV rows), `/api/search`, `/api/media/*` (details with credits and release dates), genres, providers, regions, languages, keyword and company search |
| `trakt.go` | `/api/trakt/*` — flat lists, anticipated, calendar |
| `ai.go` | `/api/ai/chat` SSE + AI settings/credentials + personal Codex and Grok OAuth flows |
| `ai_admin.go` | Admin credentials, shared Codex/Grok, AI tools, debug, external settings changes |
| `issues.go` | `/api/issues*` + `/api/admin/issues*` (+ activity) |
| `remediation.go` | Agent actions, agent runs, approval rules, remediation settings |
| `proposals.go` | `/api/admin/profile-change-proposals*` — list, detail, approve, reject |
| `arr_radarr.go` | Fake Radarr v3 behind `/api/instances/{id}/api/v3` + non-admin allowlist |
| `arr_sonarr.go` | Fake Sonarr v3 |
| `arr_chaptarr.go` | Fake Chaptarr v1 + generated MediaCover image bytes (book and author covers) |
| `arr_lidarr.go` | Fake Lidarr v1 behind `/api/instances/{id}/api/v1` (artists, albums, tracks, queue, history, wanted, calendar, releases, manual import, commands) + generated album and artist covers + the non-admin allowlist |
| `downloads.go` | `/api/downloads/{instanceID}/*` — queue, history, actions for SABnzbd and qBittorrent |
| `watchhistory.go` | `/api/watch-history/{instanceID}/*` and the `/api/tautulli/{instanceID}/*` alias — activity, history, stats for Tautulli and Tracearr |
| `mediafiles.go` | Media-file coverage, tickets, ticketed downloads |
| `notifications.go` | Push tokens + notification preferences |
| `data_movies.go` | 18-film public-domain catalog with poster/backdrop maps |
| `data_tv.go` | 6 fictional shows with seasons/episodes fixtures |
| `data_people.go` | Every person named on a title page (real cast, crew, and creators for the films; invented for the shows); credits are derived from `data_credits.go` |
| `data_credits.go` | Per-title extras: cast and crew refs, studios, countries, budget and revenue, release milestones, networks, creators |
| `data_browse.go` | Browse-filter vocabulary: watch providers by region, keywords, companies, languages, regions, and the title → attachment maps |
| `data_books.go` | Public-domain authors/books/editions/bookfiles/series + lookup corpus |
| `data_arr.go` | Arr queues, history, wanted, calendar, release fixtures |
| `data_issues.go` | Issues, threads, agent actions/runs/rules seeds |
| `data_requests.go` | Request-log rows, request policy, availability states, quality-profile fixtures |
| `data_ai.go` | Canned AI chat scripts + seeded external-settings-changes history |
| `data_music.go` | Public-domain artists/albums/tracks/track files, Lidarr queue and history fixtures, the music cross-domain hooks |
| `data_misc.go` | Genres and Trakt list fixtures |
| `assets/` | `go:embed` — sample download file, landing HTML (covers are generated PNGs, not files) |
| `tools/smoke.sh` | Read-mostly parity smoke test (about 220 checks; `--mutate` adds the create/approve/deny flows). Run it against a local or the live demo |

## Branch Workflow

This code lives on the `demo` branch. To pull in the latest changes from `main`:

```bash
git checkout demo
git merge main
git push origin demo
```

Do not merge `demo` into `main` — demo-specific code should stay on this branch.

## Cloud Deployment (DigitalOcean)

Used for App Store review periods. Spin up before submission, destroy after approval.

### Prerequisites

- `doctl` CLI authenticated (`doctl auth init`)
- SSH key registered in DigitalOcean (`doctl compute ssh-key list`)
- Note your SSH key ID for the commands below
- Cloudflare origin certificate files (`origin-cert.pem`, `origin-key.pem`) — see below

### Cloudflare Setup

One-time setup (certs are valid 15 years, reuse across deploys):

1. **Origin Certificate**: Cloudflare dashboard → SSL/TLS → Origin Server → Create Certificate
   - Hostnames: `*.cantinarr.com, cantinarr.com`
   - Validity: 15 years
   - Save as `origin-cert.pem` and `origin-key.pem` in the `demo/` directory (git-ignored)
   - **The private key is shown only once** — store it securely

2. **DNS Record**: DNS → Records → Add Record
   - Type: `A`, Name: `demo`, Content: `<DROPLET_IP>`, Proxy status: **Proxied** (orange cloud)
   - Update the IP each time you create a new droplet

3. **SSL Mode**: SSL/TLS → Overview → Set to **Full (Strict)**

4. **Authenticated Origin Pulls**: SSL/TLS → Origin Server → Enable **Authenticated Origin Pulls**

### Deploy

```bash
# Cross-compile the binary
cd demo
GOOS=linux GOARCH=amd64 go build -o cantinarr-demo .

# Create droplet ($4/mo)
doctl compute droplet create cantinarr-demo \
  --size s-1vcpu-512mb-10gb \
  --image ubuntu-24-04-x64 \
  --region sfo3 \
  --ssh-keys <YOUR_SSH_KEY_ID> \
  --tag-names demo \
  --wait \
  --format ID,Name,PublicIPv4,Status

# Upload binary and certs (replace IP)
scp cantinarr-demo origin-cert.pem origin-key.pem root@<DROPLET_IP>:/tmp/

# SSH in and set up everything
ssh root@<DROPLET_IP> << 'REMOTE'
# Move binary into place
mv /tmp/cantinarr-demo /usr/local/bin/cantinarr-demo
chmod +x /usr/local/bin/cantinarr-demo

# --- Systemd service ---
cat > /etc/systemd/system/cantinarr-demo.service << EOF
[Unit]
Description=Cantinarr Demo Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cantinarr-demo
Environment=DEMO_SERVER_URL=https://demo.cantinarr.com
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable cantinarr-demo
systemctl start cantinarr-demo

# --- Nginx reverse proxy with Cloudflare origin certs ---
apt-get update && apt-get install -y nginx

mkdir -p /etc/ssl/cloudflare
mv /tmp/origin-cert.pem /etc/ssl/cloudflare/origin-cert.pem
mv /tmp/origin-key.pem /etc/ssl/cloudflare/origin-key.pem
chmod 600 /etc/ssl/cloudflare/origin-key.pem

# Cloudflare Authenticated Origin Pull CA cert
curl -so /etc/ssl/cloudflare/cloudflare-origin-pull-ca.pem \
  https://developers.cloudflare.com/ssl/static/authenticated_origin_pull_ca.pem

cat > /etc/nginx/sites-available/cantinarr-demo << 'NGINX'
# Cloudflare IP ranges — set_real_ip_from
set_real_ip_from 173.245.48.0/20;
set_real_ip_from 103.21.244.0/22;
set_real_ip_from 103.22.200.0/22;
set_real_ip_from 103.31.4.0/22;
set_real_ip_from 141.101.64.0/18;
set_real_ip_from 108.162.192.0/18;
set_real_ip_from 190.93.240.0/20;
set_real_ip_from 188.114.96.0/20;
set_real_ip_from 197.234.240.0/22;
set_real_ip_from 198.41.128.0/17;
set_real_ip_from 162.158.0.0/15;
set_real_ip_from 104.16.0.0/13;
set_real_ip_from 104.24.0.0/14;
set_real_ip_from 172.64.0.0/13;
set_real_ip_from 131.0.72.0/22;
real_ip_header CF-Connecting-IP;

server {
    listen 80;
    server_name demo.cantinarr.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name demo.cantinarr.com;

    ssl_certificate     /etc/ssl/cloudflare/origin-cert.pem;
    ssl_certificate_key /etc/ssl/cloudflare/origin-key.pem;

    # Authenticated Origin Pulls — only accept requests from Cloudflare
    # Use "optional" initially; switch to "on" once CF propagates (~1 hour for new zones)
    ssl_client_certificate /etc/ssl/cloudflare/cloudflare-origin-pull-ca.pem;
    ssl_verify_client optional;

    # WebSocket support (download progress)
    location /api/ws {
        proxy_pass http://127.0.0.1:8484;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 120s;
        proxy_send_timeout 120s;
    }

    # SSE streaming (AI chat)
    location /api/ai/chat {
        proxy_pass http://127.0.0.1:8484;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_buffering off;
        proxy_read_timeout 300s;
    }

    # Default proxy
    location / {
        proxy_pass http://127.0.0.1:8484;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
NGINX

ln -sf /etc/nginx/sites-available/cantinarr-demo /etc/nginx/sites-enabled/cantinarr-demo
rm -f /etc/nginx/sites-enabled/default
nginx -t && systemctl restart nginx

# Firewall: SSH + HTTP/HTTPS only
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw delete allow 8484/tcp 2>/dev/null
ufw --force enable
REMOTE

# Clean up local binary
rm cantinarr-demo
```

**Note on certs**: The origin cert/key files are valid for 15 years. Store them securely and reuse across droplet deploys — no need to regenerate each time.

### Verify

```bash
# Health check via HTTPS
curl -s https://demo.cantinarr.com/api/health

# Auth test. The token field is access_token, NOT token — a script keyed on
# "token" silently reads "" and every later call runs unauthenticated, which
# looks like an empty or broken server rather than a bad script.
curl -s -X POST https://demo.cantinarr.com/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"demo"}'

# Full parity smoke test (read-only; add --mutate locally, then restart the
# demo to drop the junk it leaves in memory)
demo/tools/smoke.sh https://demo.cantinarr.com

# Confirm direct IP access is blocked (substitute the droplet's own address —
# `doctl compute droplet get cantinarr-demo --format PublicIPv4`)
curl --max-time 5 http://<droplet-ip>:8484/api/health  # should timeout

# Confirm Authenticated Origin Pulls rejects non-CF clients
curl -k --max-time 5 https://<droplet-ip>/api/health   # should fail
```

Cloudflare stamps its own `Last-Modified` on cached objects, so never read one
as a deploy timestamp — check the binary's mtime on the droplet instead.

### Tear Down

```bash
doctl compute droplet delete cantinarr-demo --force
```

Update the Cloudflare DNS A record when you create a new droplet with a different IP.
