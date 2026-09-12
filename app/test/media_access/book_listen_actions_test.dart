import 'dart:async';

import 'package:cantinarr/features/media_access/data/listen_links.dart';
import 'package:cantinarr/features/media_access/logic/listen_links_provider.dart';
import 'package:cantinarr/features/media_access/ui/book_listen_actions.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _first = ListenItem(
    id: 'one',
    title: 'The Book',
    libraryName: 'Main',
    narrators: ['First Narrator'],
    url: 'https://books.example/base/item/one');
const _second = ListenItem(
    id: 'two',
    title: 'The Book',
    libraryName: 'Other',
    narrators: ['Second Narrator'],
    url: 'https://books.example/base/item/two');

ListenLink _found(List<ListenItem> items) => ListenLink(
    instanceId: 'abs', name: 'Our books', state: 'found', items: items);

Future<void> _show(WidgetTester tester,
    FutureOr<List<ListenLink>> Function(ListenRequest) load, List<Uri> opened,
    {Widget? child}) async {
  await tester.pumpWidget(ProviderScope(
      overrides: [
        listenLinksProvider.overrideWith((ref, request) => load(request)),
        bookListenLauncherProvider.overrideWithValue((uri) async {
          opened.add(uri);
          return true;
        }),
      ],
      child: MaterialApp(
          home: Scaffold(
              body: child ??
                  const BookListenActions(
                      instanceId: 'chaptarr-a', foreignBookId: 'hc:1')))));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('an exact copy opens its public web title', (tester) async {
    final opened = <Uri>[];
    await _show(tester, (request) {
      expect(request.instanceId, 'chaptarr-a');
      expect(request.foreignId, 'hc:1');
      return [
        _found([_first])
      ];
    }, opened);
    await tester.tap(find.text('Listen in Audiobookshelf'));
    await tester.pump();
    expect(opened.single.toString(), _first.url);
    expect(find.text('Open Audiobookshelf'), findsNothing);
  });

  testWidgets('distinct copies are offered with their library and narrator',
      (tester) async {
    final opened = <Uri>[];
    await _show(
        tester,
        (_) => [
              _found([_first, _second])
            ],
        opened);
    await tester.tap(find.text('Listen in Audiobookshelf'));
    await tester.pumpAndSettle();
    expect(opened, isEmpty);
    expect(find.text('The Book'), findsNWidgets(2));
    expect(find.textContaining('Second Narrator'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('listen-item:abs:two')));
    await tester.pumpAndSettle();
    expect(opened.single.toString(), _second.url);
    expect(tester.takeException(), isNull);
  });

  for (final state in ['unverified', 'unreachable']) {
    testWidgets('$state falls back silently and rechecks on return',
        (tester) async {
      final opened = <Uri>[];
      var calls = 0;
      var verified = false;
      await _show(tester, (_) {
        calls++;
        if (verified) {
          return [
            _found([_first])
          ];
        }
        return [
          ListenLink(
              instanceId: 'abs',
              name: 'Our books',
              state: state,
              fallbackUrl: 'https://books.example/base')
        ];
      }, opened);
      expect(find.text('Listen in Audiobookshelf'), findsNothing);
      expect(find.textContaining("Couldn't"), findsNothing);
      expect(find.text('Check again'), findsNothing);
      expect(find.byType(TextButton), findsNothing);
      await tester.tap(find.text('Open Audiobookshelf'));
      await tester.pump();
      expect(opened.single.toString(), 'https://books.example/base');
      final before = calls;
      verified = true;
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pumpAndSettle();
      expect(calls, greaterThan(before));
      expect(find.text('Listen in Audiobookshelf'), findsOneWidget);
      expect(find.text('Open Audiobookshelf'), findsNothing);
    });
  }

  testWidgets('no eligible servers stays empty', (tester) async {
    await _show(tester, (_) => [], []);
    expect(find.byType(OutlinedButton), findsNothing);
  });

  testWidgets('an HTTP failure offers retry and never claims absence',
      (tester) async {
    await _show(tester, (_) => Future.error(Exception('failed')), []);
    expect(find.text("Couldn't check Audiobookshelf · Retry"), findsOneWidget);
    expect(find.text('Listen in Audiobookshelf'), findsNothing);
  });

  testWidgets(
      'a late result from a previous book cannot replace the current book',
      (tester) async {
    final old = Completer<List<ListenLink>>(),
        current = Completer<List<ListenLink>>();
    var foreignId = 'old';
    late StateSetter change;
    await _show(
        tester,
        (request) => request.foreignId == 'old' ? old.future : current.future,
        [], child: StatefulBuilder(builder: (context, setState) {
      change = setState;
      return BookListenActions(
          instanceId: 'chaptarr-a', foreignBookId: foreignId);
    }));
    change(() => foreignId = 'current');
    await tester.pump();
    old.complete([
      _found([_first])
    ]);
    await tester.pumpAndSettle();
    expect(find.text('Listen in Audiobookshelf'), findsNothing);
    current.complete([
      _found([_second])
    ]);
    await tester.pumpAndSettle();
    expect(find.text('Listen in Audiobookshelf'), findsOneWidget);
  });
}
