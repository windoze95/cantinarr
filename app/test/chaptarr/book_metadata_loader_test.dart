import 'dart:async';

import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/logic/book_metadata_loader.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

BookMetadataKey key(String id, [String instance = 'books']) =>
    (instanceId: instance, foreignId: id);
const book = ChaptarrBook(id: 0, title: 'A book', foreignBookId: 'gr:1');

class HeldMetadata {
  final calls = <BookMetadataKey>[];
  final pending = <BookMetadataKey, Completer<List<ChaptarrBook>>>{};
  final tokens = <BookMetadataKey, CancelToken>{};
  Future<List<ChaptarrBook>> fetch(BookMetadataKey key, CancelToken token) {
    calls.add(key);
    tokens[key] = token;
    return (pending[key] = Completer<List<ChaptarrBook>>()).future;
  }

  void complete(String id) => pending[key(id)]!.complete([book]);
}

Future<void> flush() => Future<void>.delayed(Duration.zero);

void main() {
  test(
      'one speculative socket, three visible candidates, foreground bypasses queue',
      () async {
    final held = HeldMetadata();
    final loader = BookMetadataLoader(held.fetch);
    addTearDown(loader.dispose);
    final owner = Object();
    loader.prefetch(owner, ['1', '2', '3', 'offscreen'].map(key));
    expect(held.calls, [key('1')]);
    final opened = loader.load(key('3'));
    expect(held.calls, [key('1'), key('3')]);
    final shared = loader.load(key('3'));
    expect(identical(opened, shared), isTrue);
    held.complete('1');
    await flush();
    expect(held.calls, [key('1'), key('3'), key('2')]);
    held.complete('3');
    held.complete('2');
    expect((await opened).books, [book]);
    await flush();
    expect(held.calls, hasLength(3));
    expect((await loader.load(key('3'))).books, [book]);
    expect(held.calls, hasLength(3));
  });

  test('closing search drops queued speculation and preserves an opened fetch',
      () async {
    final held = HeldMetadata();
    final loader = BookMetadataLoader(held.fetch);
    addTearDown(loader.dispose);
    final owner = Object();
    loader.prefetch(owner, ['1', '2', '3'].map(key));
    final opened = loader.load(key('1'));
    loader.clearPrefetch(owner);
    expect(held.tokens[key('1')]!.isCancelled, isFalse);
    held.complete('1');
    await opened;
    await flush();
    expect(held.calls, [key('1')]);
  });

  test('new viewport retires old queued books without walking either list',
      () async {
    final held = HeldMetadata();
    final loader = BookMetadataLoader(held.fetch);
    addTearDown(loader.dispose);
    final owner = Object();
    loader.prefetch(owner, ['1', '2', '3'].map(key));
    loader.prefetch(owner, ['4', '5', '6'].map(key));
    held.complete('1');
    await flush();
    expect(held.calls, [key('1'), key('4')]);
    loader.clearPrefetch(owner);
    held.complete('4');
    await flush();
    expect(held.calls, [key('1'), key('4')]);
  });

  test('cache expires after five minutes, isolates instances and evicts at 32',
      () async {
    var now = DateTime(2026);
    final calls = <BookMetadataKey>[];
    final loader = BookMetadataLoader((key, _) async {
      calls.add(key);
      return [book];
    }, now: () => now);
    addTearDown(loader.dispose);
    await loader.load(key('1'));
    await loader.load(key('1'));
    await loader.load(key('1', 'other'));
    expect(calls, [key('1'), key('1', 'other')]);
    now = now.add(const Duration(minutes: 5));
    expect(loader.peek(key('1')), isNull);
    await loader.load(key('1'));
    for (var i = 2; i <= 33; i++) {
      await loader.load(key('$i'));
    }
    expect(loader.peek(key('1')), isNull);
    expect(loader.peek(key('33')), isNotNull);
  });

  test('failures do not loop on prefetch and explicit retry bypasses them',
      () async {
    var count = 0;
    final loader = BookMetadataLoader((_, __) async {
      count++;
      if (count == 1) throw StateError('offline');
      return [book];
    });
    addTearDown(loader.dispose);
    expect((await loader.load(key('1'))).failed, isTrue);
    loader.prefetch(Object(), [key('1')]);
    expect((await loader.load(key('1'))).failed, isTrue);
    expect(count, 1);
    expect((await loader.load(key('1'), retry: true)).books, [book]);
    expect(count, 2);
  });

  testWidgets(
      'thirty-second deadline cancels a hung lookup and releases the slot',
      (tester) async {
    final held = HeldMetadata();
    final loader = BookMetadataLoader(held.fetch);
    final result = loader.load(key('1'));
    await tester.pump(const Duration(seconds: 30));
    expect((await result).failed, isTrue);
    expect(held.tokens[key('1')]!.isCancelled, isTrue);
    loader.dispose();
    await tester.pump();
  });

  test(
      'scope disposal cancels and clears work, late responses cannot populate cache',
      () async {
    final held = HeldMetadata();
    final loader = BookMetadataLoader(held.fetch);
    final result = loader.load(key('1'));
    loader.dispose();
    expect((await result).cancelled, isTrue);
    expect(held.tokens[key('1')]!.isCancelled, isTrue);
    held.complete('1');
    await flush();
    expect(loader.peek(key('1')), isNull);
    expect((await loader.load(key('2'))).cancelled, isTrue);
    expect(held.calls, [key('1')]);
  });
}
