import 'dart:async';
import 'package:cantinarr/core/models/library_sort.dart';
import 'package:cantinarr/core/storage/library_sort_preferences.dart';
import 'package:cantinarr/core/storage/preferences.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));
  test('active selection reverses and a different field starts ascending', () async {
    final notifier = LibrarySortNotifier('radarr', SharedPreferences.getInstance());
    addTearDown(notifier.dispose);
    await notifier.select(LibrarySortField.alphabetical);
    expect(notifier.state.ascending, isFalse);
    await notifier.select(LibrarySortField.added);
    expect(notifier.state, const LibrarySortSelection(field: LibrarySortField.added));
    await notifier.select(LibrarySortField.added);
    expect(notifier.state.ascending, isFalse);
  });
  test('field and direction persist independently for each module', () async {
    final first = ProviderContainer();
    addTearDown(first.dispose);
    await first.read(librarySortProvider('radarr').notifier).set(
      const LibrarySortSelection(field: LibrarySortField.added, ascending: false));
    await first.read(librarySortProvider('chaptarr').notifier).select(LibrarySortField.lastName);
    final restored = ProviderContainer();
    addTearDown(restored.dispose);
    for (final module in ['radarr', 'sonarr', 'chaptarr', 'lidarr']) {
      restored.read(librarySortProvider(module));
    }
    await pumpEventQueue();
    expect(restored.read(librarySortProvider('radarr')),
      const LibrarySortSelection(field: LibrarySortField.added, ascending: false));
    expect(restored.read(librarySortProvider('chaptarr')).field, LibrarySortField.lastName);
    expect(restored.read(librarySortProvider('sonarr')), const LibrarySortSelection());
    expect(restored.read(librarySortProvider('lidarr')), const LibrarySortSelection());
  });
  test('late load and queued writes cannot undo the newest selection', () async {
    SharedPreferences.setMockInitialValues({'library_sort_radarr': 'added:desc'});
    final load = Completer<SharedPreferences>();
    final container = ProviderContainer(overrides: [sharedPreferencesProvider.overrideWith((_) => load.future)]);
    addTearDown(container.dispose);
    final notifier = container.read(librarySortProvider('radarr').notifier);
    final one = notifier.select(LibrarySortField.size);
    final two = notifier.select(LibrarySortField.size);
    final three = notifier.select(LibrarySortField.runtime);
    load.complete(await SharedPreferences.getInstance());
    await Future.wait([one, two, three]);
    expect(notifier.state, const LibrarySortSelection(field: LibrarySortField.runtime));
    expect((await load.future).getString('library_sort_radarr'), 'runtime:asc');
  });
  test('invalid or cross-module saved fields default safely; storage failure remains usable', () async {
    for (final saved in ['unknown:asc', 'lastName:desc', 'added:wrong']) {
      expect(LibrarySortSelection.parse('radarr', saved), const LibrarySortSelection());
    }
    final notifier = LibrarySortNotifier('radarr', Future.error('unavailable'));
    addTearDown(notifier.dispose);
    await notifier.select(LibrarySortField.size);
    expect(notifier.state.field, LibrarySortField.size);
  });
}
