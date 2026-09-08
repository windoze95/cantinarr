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
        bool unavailable = false,
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
                    unavailable: unavailable,
                    onRetry: retry ?? () {}))),
      )),
    ));

void main() {
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

  testWidgets('unmatched metadata is distinguished from a failed fetch',
      (tester) async {
    await pumpSynopsis(tester, loading: false, unavailable: true);
    expect(find.text('Joan has always loved the stars…'), findsOneWidget);
    expect(find.text('The book source didn’t return matching details.'),
        findsOneWidget);
    expect(find.text('Couldn’t load more details'), findsNothing);
    expect(find.byType(TextButton), findsNothing);
    await pumpSynopsis(tester, text: '', loading: false, unavailable: true);
    expect(find.text('The book source didn’t return matching details.'),
        findsOneWidget);
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
    expect(find.text('The book source only provided this preview.'),
        findsOneWidget);
  });

  testWidgets('a warm source snippet explains its limit without a toggle',
      (tester) async {
    await pumpSynopsis(tester, loading: false);
    expect(find.text('The book source only provided this preview.'),
        findsOneWidget);
    expect(find.byType(TextButton), findsNothing);
  });

  testWidgets(
      'short complete and missing descriptions have no expansion control',
      (tester) async {
    await pumpSynopsis(tester,
        text: 'A short, complete synopsis.', loading: false);
    expect(find.byType(TextButton), findsNothing);
    await pumpSynopsis(tester, text: '');
    expect(find.text('Loading book details…'), findsOneWidget);
    await pumpSynopsis(tester, text: '', loading: false);
    expect(find.text('About this book'), findsNothing);
  });
}
