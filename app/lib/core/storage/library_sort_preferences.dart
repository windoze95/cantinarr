import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../models/library_sort.dart';
import 'preferences.dart';

final librarySortProvider = StateNotifierProvider.family<
    LibrarySortNotifier, LibrarySortSelection, String>((ref, module) =>
  LibrarySortNotifier(module, ref.read(sharedPreferencesProvider.future)));

class LibrarySortNotifier extends StateNotifier<LibrarySortSelection> {
  LibrarySortNotifier(this.module, this._preferences)
      : super(const LibrarySortSelection()) {
    _load();
  }

  final String module;
  final Future<SharedPreferences> _preferences;
  String get _key => 'library_sort_$module';
  bool _selected = false;
  Future<void> _writes = Future<void>.value();

  Future<void> _load() async {
    try {
      final preferences = await _preferences;
      if (!mounted || _selected) return;
      state = LibrarySortSelection.parse(module, preferences.getString(_key));
    } catch (_) {
      // Preferences must not prevent browsing.
    }
  }

  Future<void> select(LibrarySortField field) => set(state.select(field));

  Future<void> set(LibrarySortSelection selection) {
    _selected = true;
    final value = selection.validated(module);
    state = value;
    _writes = _writes.then((_) async {
      try {
        await (await _preferences).setString(_key, value.key);
      } catch (_) {
        // Keep the latest choice for this session when storage is unavailable.
      }
    });
    return _writes;
  }
}
