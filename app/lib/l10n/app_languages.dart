import 'package:flutter/widgets.dart';

import 'app_localizations.dart';

/// Every generated ARB locale supplies its own selector label and optional
/// provider language. Adding a translation does not require another registry.
class AppLanguage {
  AppLanguage(this.locale)
      : nativeName = lookupAppLocalizations(locale).languageNativeName,
        metadataTag = lookupAppLocalizations(locale).metadataLanguageTag;

  final Locale locale;
  final String nativeName;
  final String metadataTag;
  String get tag => locale.toLanguageTag();
}

final appLanguages = List<AppLanguage>.unmodifiable(
  AppLocalizations.supportedLocales.map(AppLanguage.new),
);

/// Resolve only within the requested language. Unknown saved preferences must
/// follow System, rather than becoming an explicit English selection.
AppLanguage? appLanguageForLocale(Locale locale) {
  final candidates = appLanguages
      .where((entry) => entry.locale.languageCode == locale.languageCode)
      .toList();
  if (candidates.isEmpty) return null;
  final resolved = basicLocaleListResolution(
      [locale], candidates.map((entry) => entry.locale));
  return candidates.firstWhere((entry) => entry.locale == resolved);
}

AppLanguage? appLanguageForTag(String tag) {
  final parts = tag.replaceAll('_', '-').split('-');
  if (parts.isEmpty || !RegExp(r'^[a-z]{2,3}$').hasMatch(parts.first)) {
    return null;
  }
  String? script, country;
  var index = 1;
  if (index < parts.length &&
      RegExp(r'^[A-Z][a-z]{3}$').hasMatch(parts[index])) {
    script = parts[index++];
  }
  if (index < parts.length &&
      RegExp(r'^(?:[A-Z]{2}|[0-9]{3})$').hasMatch(parts[index])) {
    country = parts[index++];
  }
  if (index != parts.length) return null;
  return appLanguageForLocale(Locale.fromSubtags(
      languageCode: parts.first, scriptCode: script, countryCode: country));
}

/// Variants may intentionally use the same provider language; list it once.
final metadataLanguageNames = _metadataLanguageNames();

Map<String, String> _metadataLanguageNames() {
  final names = <String, String>{};
  for (final entry in appLanguages) {
    if (entry.metadataTag.isNotEmpty) {
      names.putIfAbsent(entry.metadataTag, () => entry.nativeName);
    }
  }
  return Map.unmodifiable(names);
}
