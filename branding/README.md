# Cantinarr branding

Play Sun is the default logo and launcher icon. Play Sign, Play Dome, and Original are optional native app icons.

The round 4 v4 artwork is by Julian Poidevin, from `julian-poidevin/cantinarr-logo` at commit `010eebb`. These checked-in SVGs are the source for Cantinarr exports, so building the app does not require access to that private repository. The wordmark uses outlined Schibsted Grotesk from the supplied artwork.

- Symbols use the kit's boolean-cleaned v4 geometry. Play Dome uses its corrected tile-clipped ground, avoiding the two protruding lower side edges in the round 4 concept SVG.
- Lockups preserve the round 4 title-case lettering and spacing. Only the i-dot is split into its own path and colored Sun yellow (`#FBE15A`), matching the small sun. The `arr` accent remains Gold (`#E7A42C`).
- Native icon exports have square, opaque backgrounds; iOS and Android supply their masks. Android adaptive foregrounds leave room for launcher masks. Web favicons use the supplied small-size Play Sun variant.
- `original.png` preserves the original logo. `Original.appiconset` and Android `ic_launcher_original*` preserve the previous native artwork, including dark/tinted/themed assets.

Regenerate exports with Python 3.13:

```sh
python3 -m venv /tmp/cantinarr-branding
/tmp/cantinarr-branding/bin/pip install -r branding/requirements.txt
/tmp/cantinarr-branding/bin/python scripts/generate_branding.py
# Also update the separately checked-out marketing site:
/tmp/cantinarr-branding/bin/python scripts/generate_branding.py --site /path/to/cantinarr-site
```

The script updates Flutter, iOS, Android, macOS, Windows, web/PWA, splash/notification, authentication-page, and documentation assets. The marketing site's social card has its own SVG source in `assets/og-play-sun.svg`.
