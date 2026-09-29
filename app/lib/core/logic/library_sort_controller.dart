import 'package:flutter/foundation.dart';

import '../models/library_sort.dart';
import '../utils/library_sort.dart';

/// Auxiliary reads never prevent the title list from loading. Each refresh
/// replaces this instance's labels; superseded reads cannot put old names back.
class LibrarySortController {
  LibrarySortController({required this.module, required this.loaders,
    required this.onChanged});

  final String module;
  final Map<LibrarySortLookup, Future<Map<int, String>> Function()> loaders;
  final VoidCallback onChanged;
  LibrarySortSelection _selection = const LibrarySortSelection();
  LibrarySortSelection get selection => _selection;
  final _values = <LibrarySortLookup, Map<int, String>>{};
  final _failed = <LibrarySortLookup>{};
  final _loading = <LibrarySortLookup>{};
  int _generation = 0;
  bool _disposed = false;

  LibrarySortLabels get labels => LibrarySortLabels(_values);

  bool get _unavailable {
    final lookup = selection.field.lookup;
    return lookup != null && !_values.containsKey(lookup);
  }

  LibrarySortSelection get effectiveSelection =>
      _unavailable ? const LibrarySortSelection() : selection;

  String? get notice {
    if (!_unavailable) return null;
    final lookup = selection.field.lookup!;
    final reason = _failed.contains(lookup)
        ? 'Could not load ${lookup.label}.' : 'Loading ${lookup.label}.';
    return '$reason Showing alphabetical order until this sort is available.';
  }

  bool get canRetry => _unavailable &&
      _failed.contains(selection.field.lookup) &&
      !_loading.contains(selection.field.lookup);

  void setSelection(LibrarySortSelection value) {
    if (_disposed) return;
    final next = value.validated(module);
    if (next == _selection) return;
    _selection = next;
    onChanged();
  }

  Future<void> refresh() async {
    final generation = ++_generation;
    _values.clear();
    _failed.clear();
    _loading.addAll(loaders.keys);
    if (_disposed) return;
    onChanged();
    await Future.wait(loaders.entries.map((entry) async {
      Map<int, String>? names;
      try {
        names = await entry.value();
      } catch (_) {
        // Do not leak provider addresses or fail the independently loaded list.
      }
      if (_disposed || generation != _generation) return;
      _loading.remove(entry.key);
      if (names == null) {
        _failed.add(entry.key);
      } else {
        _values[entry.key] = names;
      }
      onChanged();
    }));
  }

  void dispose() { _disposed = true; }
}
