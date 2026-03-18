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

# Upload binary (replace IP)
scp cantinarr-demo root@<DROPLET_IP>:/usr/local/bin/cantinarr-demo

# SSH in and set up the service
ssh root@<DROPLET_IP> << 'REMOTE'
chmod +x /usr/local/bin/cantinarr-demo

cat > /etc/systemd/system/cantinarr-demo.service << EOF
[Unit]
Description=Cantinarr Demo Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cantinarr-demo
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable cantinarr-demo
systemctl start cantinarr-demo

# Firewall: SSH + demo port only
ufw allow 22/tcp
ufw allow 8484/tcp
ufw --force enable
REMOTE

# Clean up local binary
rm cantinarr-demo
```

The demo server will be available at `http://<DROPLET_IP>:8484`.

### Verify

```bash
curl -X POST http://<DROPLET_IP>:8484/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"demo"}'
```

### Tear Down

```bash
doctl compute droplet delete cantinarr-demo --force
```
