# Cantinarr Demo Server

A self-contained mock backend for App Store distribution review. Simulates the full Cantinarr API with in-memory data — no external services or databases required.

## Running

```bash
cd demo
go run .
```

The server starts on **port 8484**.

## Demo Credentials

| Username | Password | Role  |
|----------|----------|-------|
| admin    | demo     | admin |
| user     | demo     | user  |

New accounts can be registered with invite code: `DEMO42`

## What's Simulated

- Authentication (JWT login, registration, token refresh)
- Media discovery and search (movies, TV, persons)
- Download requests with progress simulation via WebSocket
- Radarr/Sonarr API proxies (quality profiles, root folders)
- Trakt integration (trending, popular, calendar, lists)
- AI chat with streaming SSE responses
- All content uses public domain films and metadata

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

# Auth test
curl -s -X POST https://demo.cantinarr.com/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"demo"}'

# Confirm direct IP access is blocked
curl --max-time 5 http://164.90.148.150:8484/api/health  # should timeout

# Confirm Authenticated Origin Pulls rejects non-CF clients
curl -k --max-time 5 https://164.90.148.150/api/health   # should fail
```

### Tear Down

```bash
doctl compute droplet delete cantinarr-demo --force
```

Update the Cloudflare DNS A record when you create a new droplet with a different IP.
