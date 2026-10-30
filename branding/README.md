# Cantinarr branding

Play Sun is the default logo and launcher icon. Play Sign, Play Dome, and Original are optional native app icons.

The round 4 v4 artwork is by Julian Poidevin, from `julian-poidevin/cantinarr-logo` at commit `010eebb`. These checked-in SVGs are the source for Cantinarr exports, so building the app does not require access to that private repository. The wordmark uses outlined Schibsted Grotesk from the supplied artwork.

- Symbols use the kit's boolean-cleaned v4 geometry. Play Dome uses its corrected tile-clipped ground, avoiding the two protruding lower side edges in the round 4 concept SVG.
- Lockups preserve the round 4 title-case lettering and spacing. Only the i-dot is split into its own path and colored Sun yellow (`#FBE15A`), matching the small sun. The `arr` accent remains Gold (`#E7A42C`).
- Native icon exports have square, opaque backgrounds; iOS and Android supply their masks. Android adaptive foregrounds leave room for launcher masks. Web favicons use the supplied small-size Play Sun variant with its charcoal tile removed. Loading splash images use the regular Play Sun artwork without its tile, over the existing Night background.
- Website headers use the kit's transparent `symbol-dark.svg`, `symbol-light.svg`, `lockup-horizontal-dark.svg`, and `lockup-horizontal-light.svg` exports from `deliverables/play-sun/svg/`, preserved here as `play-sun-web-*.svg`. Dark and light refer to the background: dark surfaces use gold and yellow, while light surfaces use charcoal and gold for contrast. These are separate from the application icon tiles.
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

To refresh only the transparent website SVGs, catalog PNG, and favicons without rendering launcher icons:

```sh
/tmp/cantinarr-branding/bin/python scripts/generate_branding.py --web-only
# Optionally update the marketing site's versioned lockup and catalog icon too:
/tmp/cantinarr-branding/bin/python scripts/generate_branding.py --web-only --site /path/to/cantinarr-site
# Refresh only the iOS, Android, and web loading splash images:
/tmp/cantinarr-branding/bin/python scripts/generate_branding.py --splash-only
```

The documentation header selects the matching symbol for its light or dark theme. Its `lockup-dark.svg` and `lockup-light.svg` exports also use the transparent artwork. The marketing site uses `lockup-transparent-v1.svg` and `symbol-transparent-v1.svg`. Its matching `symbol-transparent-v1.png` is a 1024 by 1024 RGBA export for deployment catalogs such as Unraid, with the SVG's aspect ratio preserved on a transparent canvas. Catalog templates can use `https://cantinarr.com/static/symbol-transparent-v1.png`. Use new filenames when changing these assets because the site caches static assets immutably.

The favicon and splash exporters remove only the first charcoal tile path from `play-sun-small.svg` and `play-sun.svg`, respectively. They preserve the original 256 by 256 canvas and all foreground geometry. Splash canvases keep their existing dimensions and Android 12 safe area. Documentation and marketing use `favicon-transparent-v1.png`; the app's favicon URL carries a matching cache version. Launcher, touch, PWA, notification, and in-app navigation icons remain separate exports.
