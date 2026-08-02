# Launch announcement (Reddit)

Copy for launch posts on r/selfhosted and similar communities. Adjust the intro
to taste per subreddit; the body is written to stand alone.

---

**Suggested title:**

> Cantinarr — requests for movies, TV, and books, plus a fixer for stuck downloads. Single container, AGPL.

**Body:**

I built Cantinarr because I was tired of being the human retry button for my
media server. Someone requests something, it wedges in the queue — a rar nobody
extracted, a sample file, "not an upgrade", a broken remote path mapping — and
suddenly it's my evening. Request apps solve the front half of that problem. I
wanted the back half solved too.

So: Cantinarr is a request app for movies, TV shows, and books (Radarr, Sonarr,
and Chaptarr for the Readarr-style book side), and when a download gets stuck
it tells you *why* in plain English and offers a one-click fix — manual import
with the candidate files shown, remove + blocklist + re-search, rescan, that
kind of thing. It can also watch the queue itself: it waits quietly to see if
Radarr/Sonarr recovers on its own, and only brings a proposed fix to you if the
problem sticks around. If you keep approving the same fix, tick "Always
approve" and that exact problem-and-fix pair stops paging you.

The rest of it, briefly:

- Per-season requests for shows, per-format (ebook/audiobook) requests for
  books, optional approval queues, per-user quality profiles.
- Availability is computed live from the arrs every time — there's no cached
  library snapshot to drift out of date when you edit Radarr directly, and
  webhooks push imports and deletes into the app the moment they happen.
- TMDB for discovery, with automatic TMDB→TVDB bridging (Trakt fallback) so
  Sonarr adds don't fail on missing IDs.
- Download client queues for SABnzbd, qBittorrent, NZBGet, and Transmission,
  plus full series → season → episode drill-down control of Radarr/Sonarr from
  the app.
- Household users sign in with a connect link: no passwords, no expiring
  sessions. Passkeys and passwords are opt-in, per user.
- Push notifications, Tautulli for who's-watching, and a Plex invite flow for
  onboarding new people.
- One Go binary in one container with SQLite, web app bundled, one port. Runs
  fine on a Pi or a NAS.

On the AI stuff, since I know how that lands here: yes, there's an assistant
(recommendations, "request this for me", admin queue control from chat), the
remediation agent above, and an MCP endpoint if you'd rather point your own
client at it. All of it is optional and bring-your-own-key — Anthropic, OpenAI,
Gemini, or a ChatGPT account's Codex allowance. Configure no provider and it's
simply a request manager; every AI tool also has an individual off switch, and
keys are encrypted at rest and never leave the server.

Books were honestly half the reason I built this at all: proper book automation
with a request UI in front of it that regular humans can use, including a "your
audiobook is ready" notification that actually arrives when the file does.

Try it:

    git clone https://github.com/windoze95/cantinarr.git
    cd cantinarr
    docker compose up -d

or grab `ghcr.io/windoze95/cantinarr:latest` directly.

Live demo: https://demo.cantinarr.com
Code (AGPL-3.0): https://github.com/windoze95/cantinarr
Feature requests: https://cantinarr.com/roadmap/

Happy to answer questions, and honest feedback is welcome — especially from
anyone who's been burned by stuck imports as often as I have.
