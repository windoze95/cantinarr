import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'preferences.dart';

enum LibraryViewMode { list, grid }

/// Layout is device-local and shared by every instance of the same module.
final libraryViewModeProvider = StateNotifierProvider.family<
    LibraryViewModeNotifier, LibraryViewMode, String>((ref, module) {
  return LibraryViewModeNotifier(
    module,
    ref.read(sharedPreferencesProvider.future),
  );
});

class LibraryViewModeNotifier extends StateNotifier<LibraryViewMode> {
  LibraryViewModeNotifier(String module, this._preferences)
      : _key = 'library_view_$module',
        super(LibraryViewMode.list) {
    _load();
  }

  final String _key;
  final Future<SharedPreferences> _preferences;
  bool _selected = false;
  Future<void> _writes = Future<void>.value();

  Future<void> _load() async {
    try {
      final preferences = await _preferences;
      if (!mounted || _selected) return;
      state = preferences.getString(_key) == 'grid'
          ? LibraryViewMode.grid
          : LibraryViewMode.list;
    } catch (_) {
      // An unavailable preference store must not prevent library browsing.
    }
  }

  Future<void> set(LibraryViewMode value) {
    _selected = true;
    state = value;
    // Preserve the user's last selection even during rapid toggles.
    _writes = _writes.then((_) async {
      try {
        await (await _preferences).setString(_key, value.name);
      } catch (_) {
        // Keep the selection for this session if persistence is unavailable.
      }
    });
    return _writes;
  }
}
