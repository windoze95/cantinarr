---
title: Translate Cantinarr
description: Improve a translation or add a supported language with one ARB file, without changing application code.
sidebar:
  order: 2
---

You can improve an existing translation or add a language by editing one file under `app/lib/l10n/`. Cantinarr generates its language choices from these files. You do not need to edit Dart code, a language registry, dependency files, or native project settings.

English and French are included. Contributions are reviewed by maintainers and, when available, speakers of the language. Movie and TV descriptions come from the metadata provider; translating Cantinarr's interface does not create missing provider translations.

## Before you start

- Check `app/lib/l10n/` and open pull requests for an existing translation of your language.
- Use the English `app_en.arb` file as the source. ARB is a JSON format containing message identifiers, translated text, and information about placeholders.
- Use a language supported by the repository's pinned Flutter SDK, including its Material and Cupertino widgets. CI checks this. See [Flutter's internationalization guide](https://docs.flutter.dev/ui/internationalization).
- For local checks, install the Flutter version in `app/.flutter-version`. You can also edit through GitHub and let the pull request checks validate your contribution.

## Improve an existing translation

Open its `app_<locale>.arb` file, correct the translated values, and submit a pull request. Keep the message identifiers, placeholder names, and `@message` descriptions intact. Explain any wording that needs context, especially requester actions such as Available, Requested, and Downloading.

## Add a language

1. Copy `app/lib/l10n/app_en.arb` to `app/lib/l10n/app_<locale>.arb`. For example, use `app_es.arb` for Spanish or `app_de.arb` for German.
2. Update these fields in the copied file:

   ```json
   {
     "@@locale": "es",
     "languageNativeName": "Español",
     "metadataLanguageTag": "es"
   }
   ```

   Keep the rest of the copied messages. This example shows only the fields that identify the language.
3. Translate the user-facing values. Keep every English message identifier and its parameters, including entries used on administrator screens. Translate the text inside plural/select branches without renaming their selectors.
4. Run the checks below or submit your file for CI validation. A new-language contribution needs only this ARB file; generated Dart and native bundle declarations are build outputs.

`languageNativeName` is the language's name as its speakers write it. It appears in Settings even when the surrounding interface is in another language. Give regional variants distinguishable names, such as `Français (Canada)`.

`metadataLanguageTag` selects the provider language for **Follow app language** and the **Metadata language** menu. Use an ISO 639-1 language code, optionally followed by an ISO 3166-1 country: `es`, `de`, `pt-BR`, or `fr-CA`. This value is an identifier, not text to translate. Leave it as an empty string if the interface language has no supported provider code; metadata then falls back to English. See [TMDB's language format](https://developer.themoviedb.org/docs/languages).

### Regional and script variants

Use underscores in ARB filenames and `@@locale`: for example, `app_fr_CA.arb` with `"@@locale": "fr_CA"`, or `app_zh_Hant_TW.arb` with `"@@locale": "zh_Hant_TW"`. The filename and field must agree. Flutter requires the base-language file to exist first, so start with `app_zh.arb` before contributing a Chinese variant.

The app preserves the full language, script, and region when you choose a variant. Native iOS bundle declarations are generated with hyphenated tags during the build. Provider metadata uses its own language/country format: a Traditional Chinese interface can specify `"metadataLanguageTag": "zh-TW"`; script subtags such as `Hant` do not belong in that provider field.

## Preserve placeholders and plurals

For a message containing `{title}` or `{count}`, translate the surrounding sentence and retain those parameter names. Do not translate URLs, protocol identifiers, service names, or format tokens.

For ICU plural messages, retain the `other` branch and use the plural categories appropriate to the language. Preserve named parameters in the branches that use them. For example:

```json
"exampleCount": "{count, plural, one{Un élément} other{{count} éléments}}"
```

This is a syntax example, not a new message to add. Keep existing message identifiers from the English file. Flutter checks ICU syntax during generation, and the app tests check message coverage and parameters.

## Check your contribution

From `app/`, using the pinned Flutter SDK:

```sh
flutter pub get --enforce-lockfile
flutter gen-l10n
flutter analyze --no-fatal-infos --no-pub
flutter test --no-pub
```

If generation fails, check JSON commas and quoting, matching filenames and `@@locale`, and balanced ICU braces. If a catalog test fails, its message identifies the missing key, parameter, or unsupported locale. Do not edit the generated `app_localizations*.dart` files to fix an error; update the ARB and regenerate.

CI also tests a temporary single-file language addition and the generated iOS bundle declaration. These checks run in an isolated copy and do not add sample translations to the app.

## Review the language in the app

Choose **Settings > Account > App language** and select the language by its native name. Restart the app to check the saved choice, then choose **System default** to check device-language selection. Review narrow screens, long labels, dates, plural counts, and right-to-left layouts where applicable.

Check **Metadata language > Follow app language** against a server that supports language tags. Missing provider translations retain English or source text. Older servers offer only their advertised metadata languages and can fall back to English; interface translations still work independently.

In your pull request, include the language/region, whether you are a native speaker, which screens you reviewed, and any wording you want another speaker to check. For a layout problem, include the screen name, device size, and text size. Maintainers handle building and distributing native test versions; passing CI does not establish physical-device quality.

Contributions normally target `main`; use the active release branch if a maintainer requests it. See [Build and contribute](/contributing/development/) for the repository workflow.
