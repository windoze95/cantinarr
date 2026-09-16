#!/usr/bin/env python3
"""Render canonical repository docs into the site. Never fetch content at build time."""
import hashlib
import json
import re
import subprocess
from pathlib import Path
from urllib.parse import urlsplit

SITE = Path(__file__).resolve().parents[1]
ROOT = SITE.parent
CONTENT = SITE / 'src/content/docs'
REPO = 'https://github.com/windoze95/cantinarr'
DIRECT = {
    'docs/books-setup.md': ('integrations/guides/books', 'Set up books with Chaptarr', 'Connect your book library, grant access, and request ebooks and audiobooks.'),
    'docs/music-setup.md': ('integrations/guides/music', 'Set up music with Lidarr', 'Connect Lidarr, give people access, and follow an album from request to available.'),
    'docs/oidc-setup.md': ('integrations/guides/oidc', 'Single sign-on with OIDC', 'Connect an identity provider, link existing accounts, and test sign-in before requiring it.'),
    'docs/plex-setup.md': ('integrations/guides/plex-sign-in', 'Sign in with Plex', 'Enable Plex sign-in, verify account links, and choose whether server members can sign up.'),
    'docs/apple-tv.md': ('integrations/guides/apple-tv', 'Infuse on Apple TV', 'Pair a TV, give adults access, and open a movie or show from Cantinarr.'),
    'docs/privacy-policy.md': ('reference/generated/privacy', 'Privacy policy', 'What Cantinarr stores and which services receive information when you use it.'),
    'docs/store-release.md': ('contributing/generated/releases', 'Release playbook', 'The maintainer procedure for release candidates, server images, mobile betas, and production stores.'),
}
LINKS = {key: '/' + value[0] + '/' for key, value in DIRECT.items()}
LINKS.update({'README.md': '/start/overview/', 'docs/updating.md': '/install/updates/',
              'server/README.md': '/reference/generated/api/', 'app/README.md': '/reference/generated/app/',
              'docs/testing/README.md': '/contributing/testing/'})
ANCHOR_LINKS = {}


def slug(text):
    return re.sub(r'[\s-]+', '-', re.sub(r'[^\w\s-]', '', text.lower()).strip())


def section(text, heading):
    pattern = re.compile(r'^' + re.escape(heading) + r'\s*$', re.M)
    match = pattern.search(text)
    if not match:
        raise SystemExit(f'Canonical documentation section disappeared: {heading}')
    level = len(heading) - len(heading.lstrip('#'))
    remaining = text[match.end():]
    end = re.search(r'^#{1,' + str(level) + r'} ', remaining, re.M)
    return remaining[:end.start() if end else len(remaining)].strip()


def normalize(text, source):
    # The public writing style uses sentences/parentheses, never em dashes.
    # Do not alter command-line flags or source identity strings.
    chunks = re.split(r'(```.*?```)', text, flags=re.S)
    for i in range(0, len(chunks), 2):
        chunks[i] = chunks[i].replace(' — ', '; ').replace('—', '; ').replace(' -- ', ': ')
    text = ''.join(chunks).replace(' — ', '; ').replace('—', '; ')

    def link(match):
        target = match.group(2)
        if source == 'server/README.md' and target in ANCHOR_LINKS:
            return f'[{match.group(1)}]({ANCHOR_LINKS[target]})'
        if urlsplit(target).scheme or target.startswith(('#', '/')):
            return match.group(0)
        path, _, anchor = target.partition('#')
        resolved = (ROOT / source).parent.joinpath(path).resolve()
        if not resolved.is_relative_to(ROOT):
            return match.group(0)
        relative = resolved.relative_to(ROOT).as_posix()
        # README anchors belong to the original complete file, not a split page.
        destination = LINKS.get(relative) if not anchor else None
        if not destination:
            destination = f'{REPO}/blob/main/{relative}' + ('#' + anchor if anchor else '')
        return f'[{match.group(1)}]({destination})'

    return re.sub(r'\[([^\]]+)\]\(([^\s)]+)\)', link, text)


manifest = []
written = set()


def write(path, title, description, body, source=None, order=50):
    page = CONTENT / f'{path}.md'
    page.parent.mkdir(parents=True, exist_ok=True)
    metadata = f'---\ntitle: {json.dumps(title)}\ndescription: {json.dumps(description)}\nsidebar:\n  order: {order}\n'
    if source:
        metadata += f'editUrl: {REPO}/edit/main/{source}\n'
        manifest.append({'page': path, 'source': source, 'sha256': hashlib.sha256((ROOT / source).read_bytes()).hexdigest()})
        body = normalize(body, source)
        body += f'\n\n[View the maintained source for this page]({REPO}/blob/main/{source}).\n'
    rendered = metadata + '---\n\n' + body.strip() + '\n'
    if not page.exists() or page.read_text() != rendered:
        page.write_text(rendered)
    written.add(page)


for source, (path, title, description) in DIRECT.items():
    body = re.sub(r'^# [^\n]+\n', '', (ROOT / source).read_text(), count=1).strip()
    write(path, title, description, body, source)

readme = (ROOT / 'README.md').read_text()
config = section(readme, '## Configuration')
environment = config.split('Optional server env vars for deployment tuning:', 1)[1].split('Source image builds also accept', 1)[0]
write('reference/generated/environment', 'Environment variables',
      'Deployment settings, their defaults, and which address each part of Cantinarr needs.',
      'Most settings belong in the app. These variables configure the server process. '
      'The defaults below describe the server with no override; an installation example can explicitly choose another value. '
      'See [environment variables and configuration](/install/configuration/) for choosing values, `.env` files, '
      'and applying changes. With Compose, run `docker compose up -d cantinarr` to apply environment changes; '
      '`docker compose restart` does not update the container environment.\n\n'
      + environment + '\n\n## Compatibility aliases\n\n'
      '`CANTINARR_PUBLIC_URL` remains an alias for `CANTINARR_ARR_CALLBACK_URL`; a nonempty new value wins. '
      '`CANTINARR_ANDROID_CERT_SHA256` remains an alias for `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS`; a nonempty plural value wins. '
      'Kubernetes may inject `CANTINARR_SERVICE_HOST` and `CANTINARR_SERVICE_PORT`; these are platform values, not settings to add by hand.\n\n'
      'The database lives at `/config/cantinarr.db`. There is no supported `CANTINARR_DB_PATH` setting, '
      'and current startup does not read `CANTINARR_ADMIN_PASSWORD`; create the first administrator in the setup screen. '
      'Preserve the whole `/config` directory and its encryption key. '
      'See [backups](/install/backups/) and [networking](/install/networking/) for worked examples.', 'README.md', 1)

server = (ROOT / 'server/README.md').read_text()
api = section(server, '## API Reference')
for heading in re.findall(r'^### (.+)$', api, re.M):
    original_anchor = re.sub(r'[^\w\s-]', '', heading.lower()).strip().replace(' ', '-')
    ANCHOR_LINKS['#' + original_anchor] = f'/reference/generated/api/{slug(heading)}/'
api_index = api.split('### ', 1)[0].strip() + '\n\nChoose a group below for routes, request fields, access rules, and response behavior. These pages are built from the server reference in the same checkout.\n\n'
for i, part in enumerate(re.split(r'^### ', api, flags=re.M)[1:]):
    heading, body = part.split('\n', 1)
    path = f'reference/generated/api/{slug(heading)}'
    api_index += f'- [{heading}](/{path}/)\n'
    write(path, heading, f'HTTP routes and behavior for {heading.lower()}.', body, 'server/README.md', i + 1)
write('reference/generated/api/index', 'API reference', 'The HTTP interface used by the app, integrations, and administration tools.', api_index, 'server/README.md', 0)

architecture = section(server, '## Architecture')
for i, part in enumerate(re.split(r'^### ', architecture, flags=re.M)[1:]):
    heading, body = part.split('\n', 1)
    write(f'reference/generated/architecture/{slug(heading)}', heading,
          f'Implementation contracts and behavior for {heading.lower()}.', body, 'server/README.md', i + 1)

app = (ROOT / 'app/README.md').read_text()
features = section(app, '## Features')
app_index = 'Detailed behavior for every major app surface. For task-based instructions, start with [Use Cantinarr](/use/discovery/).\n\n'
for i, part in enumerate(re.split(r'^### ', features, flags=re.M)[1:]):
    heading, body = part.split('\n', 1)
    path = f'reference/generated/app/{slug(heading)}'
    app_index += f'- [{heading}](/{path}/)\n'
    write(path, heading, f'Detailed app behavior for {heading.lower()}.', body, 'app/README.md', i + 1)
app_index += '\n## Navigation\n\n' + section(app, '## Navigation')
write('reference/generated/app/index', 'App behavior reference', 'A detailed reference to screens, navigation, and the behavior behind each control.', app_index, 'app/README.md', 0)

settings_source = 'app/lib/features/settings/data/settings_search_index.dart'
settings = (ROOT / settings_source).read_text()
screens = {}
setting_count = 0
for block in settings.split('SettingsSearchEntry(')[1:]:
    if not re.search(r'^\s+title:', block, re.M):
        continue
    def value(key):
        match = re.search(r'^\s+' + key + r''':\s*(['"])((?:\\.|(?!\1).)*)\1''', block, re.M)
        return match.group(2).replace("\\'", "'").replace('\\"', '"') if match else ''
    title, screen, area, route = (value(k) for k in ('title', 'screenTitle', 'section', 'route'))
    if not title or not screen or not route:
        raise SystemExit('A settings registry entry needs an explicit documentation mapping.')
    gate = re.search(r'gate:\s*(\w+)', block)
    audience = 'Administrator' if gate and gate.group(1) == 'gateAdmin' else 'When available to your account'
    screens.setdefault(screen, []).append((title, area or 'Main screen', audience))
    setting_count += 1
settings_body = ('This catalog uses the same labels and screen locations as Settings search in the app. '
                 'Visibility also depends on your account, connected services, and server capabilities. '
                 'Search in Cantinarr to open and highlight the control. For explanations, use the '
                 '[task-based settings directory](/reference/settings/).\n\n')
for screen, entries in screens.items():
    settings_body += f'## {screen}\n\n| Setting | Section | Availability |\n| --- | --- | --- |\n'
    for title, area, audience in entries:
        settings_body += f'| {title} | {area} | {audience} |\n'
    settings_body += '\n'
write('reference/generated/settings', 'All searchable settings',
      'Control names and screen locations from the current app, grouped by the screen that owns them.',
      settings_body, settings_source, 2)

for directory in ('reference/generated', 'integrations/guides', 'contributing/generated'):
    for old_page in (CONTENT / directory).rglob('*.md'):
        if old_page not in written:
            old_page.unlink()

try:
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
except subprocess.CalledProcessError:
    raise SystemExit('Build the docs from a Git checkout so source provenance is available.')
(SITE / 'src/generated.json').write_text(json.dumps({'revision': revision, 'settings_count': setting_count, 'sources': manifest}, indent=2) + '\n')
print(f'Synced {len(manifest)} reference pages from this checkout.')
