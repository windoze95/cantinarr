#!/usr/bin/env python3
"""Render the checked-in round 4 SVGs. Install branding/requirements.txt first."""

import argparse
import io
import json
from pathlib import Path
import shutil
import xml.etree.ElementTree as ET

from PIL import Image, ImageOps
import resvg_py

ROOT = Path(__file__).resolve().parents[1]
BRAND = ROOT / 'branding'
APP = ROOT / 'app'
NS = '{http://www.w3.org/2000/svg}'
ET.register_namespace('', NS[1:-1])


def svg(slug, *, square=False, scale=1, mono=False):
    tree = ET.parse(BRAND / f'{slug}{"-mono" if mono else ""}.svg').getroot()
    if not square and scale == 1:
        return ET.tostring(tree, encoding='unicode')
    # Full bleed native icons: the OS supplies the outer mask. Foregrounds
    # keep their meaningful artwork inside the adaptive icon safe circle.
    paths = list(tree.findall(NS + 'path'))
    for path in paths:
        tree.remove(path)
    tree.set('viewBox', '0 0 256 256')
    if not mono:
        colour = '#E7A42C' if slug == 'play-dome' else '#22221E'
        ET.SubElement(tree, NS + 'path', fill=colour, d='M0 0H256V256H0Z')
        paths = paths[1:]
        if slug == 'play-dome':
            horizon = 128 * (1 - scale) + 213 * scale
            ET.SubElement(tree, NS + 'path', fill='#22221E',
                          d=f'M0 {horizon}H256V256H0Z')
    group = ET.SubElement(tree, NS + 'g',
                          transform=f'translate({128 * (1-scale)} {128 * (1-scale)}) scale({scale})')
    for path in paths:
        group.append(path)
    return ET.tostring(tree, encoding='unicode')


def render(data, size):
    return Image.open(io.BytesIO(resvg_py.svg_to_bytes(
        svg_string=data, width=size, height=size))).convert('RGBA')


def save(image, path, size=None, opaque=False):
    path.parent.mkdir(parents=True, exist_ok=True)
    if size:
        image = image.resize((size, size), Image.Resampling.LANCZOS)
    if opaque:
        image = image.convert('RGB')
    image.save(path)


def transparent_svg(slug):
    """Remove only the charcoal tile, retaining the original artwork and canvas."""
    tree = ET.parse(BRAND / f'{slug}.svg').getroot()
    tile = tree.find(NS + 'path')
    if tile is None or tile.get('fill') != '#22221E':
        raise ValueError(f'{slug} does not start with the expected charcoal tile')
    tree.remove(tile)
    return ET.tostring(tree, encoding='unicode')


def export_web_assets(site=None):
    """Export transparent website artwork without changing launcher icons."""
    docs = ROOT / 'docs-site/public'
    for theme in ('dark', 'light'):
        shutil.copyfile(BRAND / f'play-sun-web-symbol-{theme}.svg',
                        docs / f'symbol-{theme}.svg')
        shutil.copyfile(BRAND / f'play-sun-web-lockup-{theme}.svg',
                        docs / f'lockup-{theme}.svg')
    if site:
        shutil.copyfile(BRAND / 'play-sun-web-lockup-dark.svg',
                        site / 'public/static/lockup-transparent-v1.svg')
        shutil.copyfile(BRAND / 'play-sun-web-symbol-dark.svg',
                        site / 'public/static/symbol-transparent-v1.svg')
    favicon = render(transparent_svg('play-sun-small'), 256)
    save(favicon, APP / 'web/favicon.png', 48)
    save(favicon, docs / 'favicon-transparent-v1.png', 48)
    if site:
        save(favicon, site / 'public/static/favicon-transparent-v1.png', 48)


def export_splash_assets():
    """Render the untiled mark into the existing native and web launch canvases."""
    splash = render(transparent_svg('play-sun'), 1024)
    save(splash, APP / 'assets/splash_icon.png')
    ios = APP / 'ios/Runner/Assets.xcassets'
    res = APP / 'android/app/src/main/res'
    launch = list((APP / 'web/splash/img').glob('*.png'))
    launch += list((ios / 'LaunchImage.imageset').glob('*.png'))
    launch += list(res.glob('drawable*/splash.png'))
    launch += list(res.glob('drawable*/android12splash.png'))
    for path in launch:
        old = Image.open(path)
        canvas = Image.new('RGBA', old.size)
        # Android 12 clips a circular region in the splash canvas.
        size = int(min(old.size) * (2 / 3 if path.name == 'android12splash.png' else 1))
        icon = splash.resize((size, size), Image.Resampling.LANCZOS)
        canvas.alpha_composite(icon, ((old.width-size)//2, (old.height-size)//2))
        save(canvas, path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--site', type=Path, help='Optional cantinarr-site checkout')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--web-only', action='store_true',
                      help='Export transparent website SVGs and favicons without rendering launcher icons')
    mode.add_argument('--splash-only', action='store_true',
                      help='Render only the native and web loading splash images')
    args = parser.parse_args()
    if args.web_only:
        export_web_assets(args.site)
        print('Exported transparent Play Sun website artwork.')
        return
    if args.splash_only:
        export_splash_assets()
        print('Rendered transparent Play Sun loading splash images.')
        return
    tiles = {slug: render(svg(slug), 1024) for slug in ('play-sun', 'play-sign', 'play-dome')}
    primary = tiles['play-sun']
    square = render(svg('play-sun', square=True), 1024)
    for slug, image in tiles.items():
        save(image, APP / f'assets/app_icon_{slug.replace("-", "_")}.png', 256)
    save(Image.open(BRAND / 'original.png'), APP / 'assets/app_icon_original.png', 256)
    wordmark = ET.parse(BRAND / 'play-sun-lockup-dark.svg').getroot()
    paths = list(wordmark.findall(NS + 'path'))
    for path in paths[:-3]:
        wordmark.remove(path)
    wordmark.set('viewBox', '300 64 641 120')
    (APP / 'assets/wordmark.png').write_bytes(resvg_py.svg_to_bytes(
        svg_string=ET.tostring(wordmark, encoding='unicode'), width=1282, height=240))
    for path in ('app/assets/logo.png', 'docs-site/public/icon.png', 'server/internal/auth/auth_logo.png'):
        save(primary, ROOT / path)
    for size in (192, 512):
        save(primary, APP / f'web/icons/Icon-{size}.png', size)
        save(render(svg('play-sun', square=True, scale=.8), size),
             APP / f'web/icons/Icon-maskable-{size}.png', opaque=True)
    save(square, APP / 'web/icons/Icon-apple-touch.png', 180, opaque=True)
    save(square, APP / 'android/fastlane/metadata/android/en-US/images/icon.png', 512, opaque=True)
    for path in (APP / 'macos/Runner/Assets.xcassets/AppIcon.appiconset').glob('app_icon_*.png'):
        save(primary, path, int(path.stem.rsplit('_', 1)[1]))
    primary.save(APP / 'windows/runner/resources/app_icon.ico', sizes=[(s, s) for s in (16, 32, 48, 64, 128, 256)])

    ios = APP / 'ios/Runner/Assets.xcassets'
    contents = json.loads((ios / 'AppIcon.appiconset/Contents.json').read_text())
    for slug, name in [('play-sun', 'AppIcon'), ('play-sign', 'PlaySign'), ('play-dome', 'PlayDome')]:
        directory = ios / f'{name}.appiconset'
        image = render(svg(slug, square=True), 1024).convert('RGB')
        save(image, directory / 'appicon.png')
        save(image, directory / 'appicon-dark.png')
        save(ImageOps.grayscale(image).convert('RGB'), directory / 'appicon-tint-bg.png')
        (directory / 'Contents.json').write_text(json.dumps(contents, indent=2) + '\n')

    res = APP / 'android/app/src/main/res'
    for slug, name in [('play-sun', 'ic_launcher'), ('play-sign', 'ic_launcher_play_sign'), ('play-dome', 'ic_launcher_play_dome')]:
        foreground = render(svg(slug, square=True, scale=.64), 432)
        monochrome = render(svg(slug, scale=.64, mono=True), 432)
        for density, factor in [('mdpi', 1), ('hdpi', 1.5), ('xhdpi', 2), ('xxhdpi', 3), ('xxxhdpi', 4)]:
            directory = res / f'mipmap-{density}'
            save(tiles[slug], directory / f'{name}.png', int(48 * factor))
            save(foreground, directory / f'{name}_foreground.png', int(108 * factor))
            save(monochrome, directory / f'{name}_monochrome.png', int(108 * factor))
        (res / f'mipmap-anydpi-v26/{name}.xml').write_text(
            '<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">\n'
            f'    <background android:drawable="@color/{name}_background"/>\n'
            f'    <foreground android:drawable="@mipmap/{name}_foreground"/>\n'
            f'    <monochrome android:drawable="@mipmap/{name}_monochrome"/>\n'
            '</adaptive-icon>\n')
        colour = '#E7A42C' if slug == 'play-dome' else '#22221E'
        (res / f'values/{name}_background.xml').write_text(
            f'<resources><color name="{name}_background">{colour}</color></resources>\n')

    export_splash_assets()
    # Notification status icons need a single opaque silhouette on transparency.
    mono = render(svg('play-sun', mono=True), 96)
    for path in res.glob('drawable*/ic_stat_notify.png'):
        save(mono, path, Image.open(path).width)

    export_web_assets(args.site)
    if args.site:
        static = args.site / 'public/static'
        save(primary, static / 'logo.png')
        save(primary, static / 'icon-512.png', 512)
        save(square, static / 'apple-touch-icon.png', 180, opaque=True)
    print('Rendered Play Sun defaults and Play Sign / Play Dome alternatives.')


if __name__ == '__main__':
    main()
