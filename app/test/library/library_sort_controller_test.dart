import 'dart:async';
import 'package:cantinarr/core/logic/library_sort_controller.dart';
import 'package:cantinarr/core/models/library_sort.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('lookup failure explains fallback, retains selection, and supports retry', () async {
    var failing = true;
    final controller = LibrarySortController(module: 'radarr', onChanged: () {},
      loaders: {LibrarySortLookup.qualityProfiles: () async {
        if (failing) throw StateError('private provider URL');
        return {1: 'Standard'};
      }});
    addTearDown(controller.dispose);
    const saved = LibrarySortSelection(field: LibrarySortField.qualityProfile, ascending: false);
    controller.setSelection(saved);
    await controller.refresh();
    expect(controller.selection, saved);
    expect(controller.effectiveSelection, const LibrarySortSelection());
    expect(controller.notice, contains('Could not load quality profiles'));
    expect(controller.notice, isNot(contains('private')));
    expect(controller.canRetry, isTrue);
    controller.setSelection(const LibrarySortSelection(field: LibrarySortField.size));
    expect(controller.notice, isNull);
    expect(controller.effectiveSelection.field, LibrarySortField.size);
    controller.setSelection(saved);
    failing = false;
    await controller.refresh();
    expect(controller.effectiveSelection, saved);
    expect(controller.notice, isNull);
  });
  test('late refresh cannot overwrite newer labels or the current selection', () async {
    final old = Completer<Map<int, String>>();
    var calls = 0;
    final controller = LibrarySortController(module: 'radarr', onChanged: () {},
      loaders: {LibrarySortLookup.tags: () => ++calls == 1 ? old.future : Future.value({1: 'New'})});
    addTearDown(controller.dispose);
    final first = controller.refresh();
    await controller.refresh();
    controller.setSelection(const LibrarySortSelection(field: LibrarySortField.tags, ascending: false));
    old.complete({1: 'Old'});
    await first;
    expect(controller.labels.name(LibrarySortLookup.tags, 1), 'New');
    expect(controller.effectiveSelection.ascending, isFalse);
  });
  test('disposed controllers ignore reads from the previous instance', () async {
    final read = Completer<Map<int, String>>();
    var changes = 0;
    final controller = LibrarySortController(module: 'lidarr', onChanged: () => changes++,
      loaders: {LibrarySortLookup.tags: () => read.future});
    final work = controller.refresh();
    final before = changes;
    controller.dispose();
    read.complete({1: 'Late'});
    await work;
    expect(changes, before);
    expect(controller.labels.name(LibrarySortLookup.tags, 1), isNull);
  });
}
