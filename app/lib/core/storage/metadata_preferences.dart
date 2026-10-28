import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../features/auth/logic/auth_provider.dart';
import 'locale_preferences.dart';

const _metadataLanguageKey = 'metadata_language';
const supportedMetadataLanguages = ['en-US', 'fr-FR'];

/// Null follows the app language. This preference belongs to this device,
/// including after signing out or connecting to an older server.
class MetadataLanguageNotifier extends StateNotifier<String?> {
  MetadataLanguageNotifier() : super(null) {
    _load();
  }
  int _revision = 0;
  Future<void> _pendingWrite = Future<void>.value();

  Future<void> _load() async {
    final revision = _revision;
    final preferences = await SharedPreferences.getInstance();
    if (!mounted || revision != _revision) return;
    final language = preferences.getString(_metadataLanguageKey);
    if (supportedMetadataLanguages.contains(language)) state = language;
  }

  Future<void> setLanguage(String? language) {
    if (language != null && !supportedMetadataLanguages.contains(language)) {
      throw ArgumentError.value(
          language, 'language', 'Unsupported metadata language');
    }
    _revision++;
    state = language;
    final write = _pendingWrite.then((_) async {
      final preferences = await SharedPreferences.getInstance();
      if (language == null) {
        await preferences.remove(_metadataLanguageKey);
      } else {
        await preferences.setString(_metadataLanguageKey, language);
      }
    });
    _pendingWrite = write.catchError((Object _) {});
    return write;
  }
}

final metadataLanguageProvider =
    StateNotifierProvider<MetadataLanguageNotifier, String?>(
  (_) => MetadataLanguageNotifier(),
);

/// Explicitly opt in only when the connected server advertises support.
/// Old web clients' automatic Accept-Language header never changes metadata.
final effectiveMetadataLanguageProvider = Provider<String?>((ref) {
  final supported = ref.watch(authProvider.select(
    (state) =>
        state.valueOrNull?.connection?.metadataLanguages ?? const <String>[],
  ));
  if (supported.isEmpty) return null;
  final selected = ref.watch(metadataLanguageProvider) ??
      (ref.watch(effectiveAppLocaleProvider).languageCode == 'fr'
          ? 'fr-FR'
          : 'en-US');
  return supported.contains(selected) ? selected : null;
});
