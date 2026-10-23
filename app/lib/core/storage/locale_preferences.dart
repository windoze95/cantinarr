import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

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
    if (language == 'en' || language == 'fr') state = Locale(language!);
  }

  Future<void> setLocale(Locale? locale) {
    final language = locale?.languageCode;
    if (language != null && language != 'en' && language != 'fr') {
      throw ArgumentError.value(locale, 'locale', 'Unsupported app language');
    }
    _revision++;
    state = language == null ? null : Locale(language);
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
