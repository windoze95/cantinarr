#!/usr/bin/env python3
"""Validate the published output, not just the Markdown that produced it."""
import hashlib
import json
import re
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urljoin, urlsplit

SITE = Path(__file__).resolve().parents[1]
ROOT = SITE.parent
DIST = SITE / 'dist'
ORIGIN = 'https://docs.cantinarr.com'
errors = []


class Page(HTMLParser):
    def __init__(self, file):
        super().__init__(convert_charrefs=True)
        self.file = file
        self.ids, self.links, self.assets = set(), [], []
        self.h1 = 0
        self.description = ''
        self.canonical = ''
        self.ignored = 0
        self.content = []
        self.feed(file.read_text())

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag in ('script', 'style'):
            self.ignored += 1
        if attrs.get('id'):
            self.ids.add(attrs['id'])
        if tag == 'h1':
            self.h1 += 1
        if tag == 'a' and 'href' in attrs:
            self.links.append(attrs['href'])
        if tag == 'meta' and attrs.get('name') == 'description':
            self.description = attrs.get('content', '')
        if tag == 'link' and attrs.get('rel') == 'canonical':
            self.canonical = attrs.get('href', '')
        if tag == 'img' and 'alt' not in attrs:
            errors.append(f'{self.file.relative_to(DIST)}: image has no alt attribute')
        if tag in ('img', 'script') and attrs.get('src'):
            self.assets.append(attrs['src'])
        if tag == 'link' and attrs.get('rel') in ('stylesheet', 'icon', 'preload'):
            self.assets.append(attrs.get('href', ''))

    def handle_endtag(self, tag):
        if tag in ('script', 'style'):
            self.ignored = max(0, self.ignored - 1)

    def handle_data(self, data):
        if not self.ignored:
            self.content.append(data)


def route(file):
    value = '/' + file.relative_to(DIST).as_posix()
    return value[:-10] if value.endswith('index.html') else value


pages = {route(file): Page(file) for file in sorted(DIST.rglob('*.html')) if 'pagefind' not in file.parts}
if '/' not in pages:
    raise SystemExit('No built homepage. Run npm run build first.')

checked_links = 0
for path, page in pages.items():
    if page.h1 != 1:
        errors.append(f'{path}: expected one main heading, found {page.h1}')
    if not page.description.strip():
        errors.append(f'{path}: missing description')
    expected_canonical = ORIGIN + ('/404/' if path == '/404.html' else path)
    if page.canonical != expected_canonical:
        errors.append(f'{path}: wrong canonical URL {page.canonical}')
    visible = ''.join(page.content)
    if '\u2014' in visible:
        examples = [line.strip()[:160] for line in visible.splitlines() if '\u2014' in line]
        errors.append(f'{path}: em dash in visible text: {examples[:2]}')
    for href in page.links + page.assets:
        target = urlsplit(urljoin(ORIGIN + path, href))
        if target.scheme not in ('http', 'https') or target.netloc != 'docs.cantinarr.com':
            continue
        checked_links += 1
        target_path = unquote(target.path)
        destination = pages.get(target_path) or pages.get(target_path.rstrip('/') + '/')
        asset = DIST / target_path.lstrip('/')
        if not destination and not asset.is_file():
            errors.append(f'{path}: missing local target {href}')
        elif destination and target.fragment and unquote(target.fragment) not in destination.ids:
            errors.append(f'{path}: missing fragment {href}')

required = [
    'start/overview', 'start/quickstart', 'start/for-households',
    'install/docker', 'install/platforms', 'install/linux', 'install/networking',
    'install/remote-access', 'install/backups', 'install/updates',
    'use/discovery', 'use/requests', 'use/status', 'use/books-music', 'use/playback',
    'use/apps', 'use/account', 'use/assistant', 'use/report-problem',
    'admin/users', 'admin/instances', 'admin/kids', 'admin/request-policy',
    'admin/issues', 'admin/remediation', 'admin/ai', 'admin/file-downloads',
    'admin/tv-matches', 'admin/configuration-history', 'admin/modules', 'admin/security',
    'integrations/radarr-sonarr', 'integrations/download-clients', 'integrations/media-servers',
    'integrations/audiobookshelf', 'integrations/instant-updates', 'integrations/push',
    'integrations/discord', 'integrations/monitoring', 'integrations/tdarr',
    'integrations/mcp', 'integrations/outbound-proxy', 'integrations/discovery-providers',
    'integrations/guides/books', 'integrations/guides/music', 'integrations/guides/oidc',
    'integrations/guides/plex-sign-in', 'integrations/guides/apple-tv',
    'troubleshooting/connections', 'troubleshooting/sign-in', 'troubleshooting/missing-content',
    'troubleshooting/requests', 'troubleshooting/downloads', 'troubleshooting/playback',
    'troubleshooting/notifications', 'troubleshooting/ai', 'troubleshooting/get-help',
    'reference/settings', 'reference/glossary', 'reference/permissions', 'reference/coverage',
    'reference/generated/api', 'reference/generated/environment', 'reference/generated/settings',
    'reference/generated/privacy', 'contributing/development', 'contributing/testing',
    'contributing/documentation', 'contributing/generated/releases',
]
for path in required:
    if '/' + path + '/' not in pages:
        errors.append(f'Missing required topic: {path}')

generated = json.loads((SITE / 'src/generated.json').read_text())
for record in generated['sources']:
    actual = hashlib.sha256((ROOT / record['source']).read_bytes()).hexdigest()
    if actual != record['sha256']:
        errors.append(f'Stale reference source: {record["source"]}')

environment_page = pages.get('/reference/generated/environment/')
if environment_page:
    environment = ''.join(environment_page.content)
    config = (ROOT / 'server/internal/config/config.go').read_text()
    for name in sorted(set(re.findall(r'"(CANTINARR_[A-Z_]+)"', config))):
        if name not in environment:
            errors.append(f'Undocumented server configuration: {name}')

tool_doc = ''.join(pages['/reference/generated/architecture/mcp-tools/'].content)
for file in (ROOT / 'server/internal/mcp').glob('*.go'):
    if file.name.endswith('_test.go') or file.name.startswith('agent_'):
        continue
    for tool in re.findall(r'Name:\s*"([a-z_]+)"', file.read_text()):
        if tool not in tool_doc:
            errors.append(f'Undocumented AI tool: {tool}')

catalog = ''.join(pages['/reference/generated/settings/'].content).replace('\u2019', "'")
registry = (ROOT / 'app/lib/features/settings/data/settings_search_index.dart').read_text()
setting_titles = re.findall(r'''^\s+title:\s*(['"])((?:\\.|(?!\1).)*)\1''', registry, re.M)
if len(setting_titles) != generated['settings_count']:
    errors.append('The searchable-settings catalog dropped a registry entry')
for quote, title in setting_titles:
    if title.replace("\\'", "'").replace('\\"', '"') not in catalog:
        errors.append(f'Missing setting from catalog: {title}')

for file in ['pagefind/pagefind.js', 'pagefind/pagefind-entry.json', 'sitemap-index.xml',
             'robots.txt', '_headers', 'favicon.png', 'licenses/Fraunces-OFL.txt',
             'licenses/SchibstedGrotesk-OFL.txt', 'licenses/IBMPlexMono-OFL.txt']:
    if not (DIST / file).is_file():
        errors.append(f'Missing publication asset: {file}')

if errors:
    print('\n'.join(sorted(set(errors))))
    raise SystemExit(f'Documentation validation failed with {len(set(errors))} error(s).')

print(f'Verified {len(pages)} pages, {checked_links} local links/assets, '
      f'{len(generated["sources"])} synchronized references, '
      f'{generated["settings_count"]} searchable settings, and required topic coverage.')
