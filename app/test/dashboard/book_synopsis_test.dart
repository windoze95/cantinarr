import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/dashboard/ui/book_synopsis.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

final full = List.generate(
        12, (i) => 'Paragraph $i describes another part of the book in full.')
    .join('\n\n');

Future<void> pumpSynopsis(WidgetTester tester,
        {String text = 'Joan has always loved the stars…',
        bool loading = true,
        bool failed = false,
        VoidCallback? retry,
        double width = 320,
        double scale = 1}) =>
    tester.pumpWidget(MaterialApp(
      theme: AppTheme.dark,
      home: Scaffold(
          body: MediaQuery(
        data: MediaQueryData(textScaler: TextScaler.linear(scale)),
        child: SingleChildScrollView(
            child: SizedBox(
                width: width,
                child: BookSynopsis(
                    key: const ValueKey('same-book'),
                    text: text,
                    loading: loading,
                    failed: failed,
                    onRetry: retry ?? () {}))),
      )),
    ));

void main() {
  testWidgets('a completed fetch leaves the collapsed preview in place',
      (tester) async {
    await pumpSynopsis(tester);
    final preview = find.byKey(const ValueKey('book-synopsis-text'));
    final before = tester.getRect(preview);
    final buttonBefore = tester.getRect(find.text('Read more'));
    await pumpSynopsis(tester, text: full, loading: false);
    expect(
        tester.widget<Text>(preview).data, 'Joan has always loved the stars…');
    expect(tester.getRect(preview), before);
    expect(tester.getRect(find.text('Read more')), buttonBefore);
    await tester.tap(find.text('Read more'));
    await tester.pump();
    expect(tester.widget<Text>(preview).data, full);
    expect(tester.widget<Text>(preview).maxLines, isNull);
    await tester.ensureVisible(find.text('Read less'));
    await tester.tap(find.text('Read less'));
    await tester.pump();
    expect(tester.widget<Text>(preview).data, full);
    expect(tester.widget<Text>(preview).maxLines, 4);
  });

  testWidgets(
      'Read more during loading expands the fetched version without another tap',
      (tester) async {
    await pumpSynopsis(tester);
    expect(find.text('Read more'), findsOneWidget);
    expect(find.text('Loading book details…'), findsNothing);
    await tester.tap(find.text('Read more'));
    await tester.pump();
    expect(find.text('Loading book details…'), findsOneWidget);
    await pumpSynopsis(tester, text: full, loading: false);
    expect(
        tester
            .widget<Text>(find.byKey(const ValueKey('book-synopsis-text')))
            .maxLines,
        isNull);
    expect(find.text('Read less'), findsOneWidget);
    expect(find.text('Loading book details…'), findsNothing);
    await tester.ensureVisible(find.text('Read less'));
    await tester.tap(find.text('Read less'));
    await tester.pump();
    expect(
        tester
            .widget<Text>(find.byKey(const ValueKey('book-synopsis-text')))
            .maxLines,
        4);
  });

  for (final width in [320.0, 1100.0]) {
    for (final scale in [1.0, 3.0]) {
      testWidgets(
          'background completion stays collapsed at width $width scale $scale',
          (tester) async {
        tester.view.physicalSize = Size(width, 1100);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        await pumpSynopsis(tester, width: width, scale: scale);
        await pumpSynopsis(tester,
            text: full, loading: false, width: width, scale: scale);
        expect(
            tester
                .widget<Text>(find.byKey(const ValueKey('book-synopsis-text')))
                .maxLines,
            4);
        expect(find.text('Read more'), findsOneWidget);
        expect(tester.takeException(), isNull);
      });
    }
  }

  testWidgets('failed enrichment preserves prose and has a working retry',
      (tester) async {
    var retries = 0;
    await pumpSynopsis(tester,
        loading: false, failed: true, retry: () => retries++);
    expect(find.text('Joan has always loved the stars…'), findsOneWidget);
    expect(find.text('Couldn’t load more details'), findsOneWidget);
    await tester.tap(find.text('Retry'));
    expect(retries, 1);
  });

  testWidgets('failed loading retires a toggle that cannot reveal more text',
      (tester) async {
    await pumpSynopsis(tester);
    await tester.tap(find.text('Read more'));
    await tester.pump();
    await pumpSynopsis(tester, loading: false, failed: true);
    expect(find.text('Read less'), findsNothing);
    expect(find.text('Read more'), findsNothing);
    expect(find.text('Retry'), findsOneWidget);
    // Text that really overflows still supports expanding and collapsing.
    await pumpSynopsis(tester, text: full, loading: false, failed: true);
    expect(find.text('Read less'), findsOneWidget);
  });

  testWidgets('completed metadata adds no source notices to the synopsis',
      (tester) async {
    await pumpSynopsis(tester, loading: false);
    expect(find.text('Joan has always loved the stars…'), findsOneWidget);
    expect(find.textContaining('The book source'), findsNothing);
    expect(find.text('Couldn’t load more details'), findsNothing);
    expect(find.byType(TextButton), findsNothing);
    await pumpSynopsis(tester, text: '', loading: false);
    expect(find.text('About this book'), findsNothing);
  });

  testWidgets(
      'an unchanged source snippet retires an ineffective expansion control',
      (tester) async {
    await pumpSynopsis(tester);
    await tester.tap(find.text('Read more'));
    await tester.pump();
    await pumpSynopsis(tester, loading: false);
    expect(find.text('Read more'), findsNothing);
    expect(find.text('Read less'), findsNothing);
    expect(find.textContaining('The book source'), findsNothing);
  });

  testWidgets('a warm source snippet has no ineffective toggle or notice',
      (tester) async {
    await pumpSynopsis(tester, loading: false);
    expect(find.textContaining('The book source'), findsNothing);
    expect(find.byType(TextButton), findsNothing);
  });

  testWidgets(
      'short complete and missing descriptions have no expansion control',
      (tester) async {
    await pumpSynopsis(tester,
        text: 'A short, complete synopsis.', loading: false);
    expect(find.byType(TextButton), findsNothing);
    await pumpSynopsis(tester, text: '');
    expect(find.text('Loading book details…'), findsNothing);
    expect(find.text('About this book'), findsNothing);
    await pumpSynopsis(tester, text: '', loading: false);
    expect(find.text('About this book'), findsNothing);
  });
}
