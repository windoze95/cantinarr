import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../l10n/app_localizations.dart';
import '../../l10n/app_languages.dart';

const _localeKey = 'app_language';

/// A device-local UI language. Null follows the device, with English as the
/// fallback for languages the app does not support. Catalog filters and the
/// device's country are independent of this preference.
class AppLocaleNotifier extends StateNotifier<Locale?> {
  AppLocaleNotifier() : super(null) {
    _load();
  }

  int _revision = 0;
  Future<void> _pendingWrite = Future<void>.value();

  Future<void> _load() async {
    final revision = _revision;
    final preferences = await SharedPreferences.getInstance();
    if (!mounted || revision != _revision) return;
    final language = preferences.getString(_localeKey);
    if (language != null) state = appLanguageForTag(language)?.locale;
  }

  Future<void> setLocale(Locale? locale) {
    final selected = locale == null ? null : appLanguageForLocale(locale);
    if (locale != null && selected == null) {
      throw ArgumentError.value(locale, 'locale', 'Unsupported app language');
    }
    final language = selected?.tag;
    _revision++;
    state = selected?.locale;
    // Serialize rapid selections so disk always ends with the latest choice.
    final write = _pendingWrite.then((_) async {
      final preferences = await SharedPreferences.getInstance();
      if (language == null) {
        await preferences.remove(_localeKey);
      } else {
        await preferences.setString(_localeKey, language);
      }
    });
    _pendingWrite = write.catchError((Object _) {});
    return write;
  }
}

final appLocaleProvider = StateNotifierProvider<AppLocaleNotifier, Locale?>(
  (_) => AppLocaleNotifier(),
);

/// The same supported locale is used by widgets and metadata preferences.
/// Observe the full platform list so System follows changes while open.
final platformLocalesProvider = Provider<List<Locale>>((ref) {
  final binding = WidgetsBinding.instance;
  final observer = _LocaleObserver(ref.invalidateSelf);
  binding.addObserver(observer);
  ref.onDispose(() => binding.removeObserver(observer));
  return List.unmodifiable(binding.platformDispatcher.locales);
});

final effectiveAppLocaleProvider = Provider<Locale>((ref) {
  final selected = ref.watch(appLocaleProvider);
  return basicLocaleListResolution(
    selected == null ? ref.watch(platformLocalesProvider) : [selected],
    AppLocalizations.supportedLocales,
  );
});

class _LocaleObserver extends WidgetsBindingObserver {
  _LocaleObserver(this.changed);
  final VoidCallback changed;
  @override
  void didChangeLocales(List<Locale>? locales) => changed();
}
