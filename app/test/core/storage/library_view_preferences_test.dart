import 'dart:async';

import 'package:cantinarr/core/storage/library_view_preferences.dart';
import 'package:cantinarr/core/storage/preferences.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('module choices persist independently across app sessions', () async {
    final first = ProviderContainer();
    addTearDown(first.dispose);
    for (final module in ['radarr', 'sonarr', 'chaptarr', 'lidarr']) {
      expect(first.read(libraryViewModeProvider(module)), LibraryViewMode.list);
    }
    await first.read(libraryViewModeProvider('radarr').notifier)
        .set(LibraryViewMode.grid);
    await first.read(libraryViewModeProvider('chaptarr').notifier)
        .set(LibraryViewMode.grid);
    final restored = ProviderContainer();
    addTearDown(restored.dispose);
    for (final module in ['radarr', 'sonarr', 'chaptarr', 'lidarr']) {
      restored.read(libraryViewModeProvider(module));
    }
    await pumpEventQueue();
    expect(restored.read(libraryViewModeProvider('radarr')), LibraryViewMode.grid);
    expect(restored.read(libraryViewModeProvider('chaptarr')), LibraryViewMode.grid);
    expect(restored.read(libraryViewModeProvider('sonarr')), LibraryViewMode.list);
    expect(restored.read(libraryViewModeProvider('lidarr')), LibraryViewMode.list);
  });

  test('late load and queued writes cannot undo the latest selection', () async {
    SharedPreferences.setMockInitialValues({'library_view_radarr': 'grid'});
    final load = Completer<SharedPreferences>();
    final container = ProviderContainer(overrides: [
      sharedPreferencesProvider.overrideWith((_) => load.future),
    ]);
    addTearDown(container.dispose);
    final provider = libraryViewModeProvider('radarr');
    final notifier = container.read(provider.notifier);
    final first = notifier.set(LibraryViewMode.grid);
    final last = notifier.set(LibraryViewMode.list);
    load.complete(await SharedPreferences.getInstance());
    await Future.wait([first, last]);
    expect(container.read(provider), LibraryViewMode.list);
    expect((await load.future).getString('library_view_radarr'), 'list');
  });

  test('unknown saved values and unavailable storage keep browsing usable', () async {
    SharedPreferences.setMockInitialValues({'library_view_radarr': 'unknown'});
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final provider = libraryViewModeProvider('radarr');
    container.read(provider);
    await pumpEventQueue();
    expect(container.read(provider), LibraryViewMode.list);
    final failed = LibraryViewModeNotifier('radarr', Future.error('unavailable'));
    addTearDown(failed.dispose);
    await failed.set(LibraryViewMode.grid);
    expect(failed.state, LibraryViewMode.grid);
  });
}
