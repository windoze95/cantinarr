import 'package:cantinarr/features/chaptarr/ui/widgets/book_link_chips.dart';
import 'package:cantinarr/features/media_detail/logic/title_links.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// The chips themselves: a tap hands the exact page to the launcher, and a
/// page that could not open is said so, never left silent.
void main() {
  const links = [
    TitleLink('Goodreads', 'https://www.goodreads.com/book/show/5907'),
    TitleLink('Open Library', 'https://openlibrary.org/works/OL262758W'),
  ];

  testWidgets('tapping a chip launches that site page and nothing else',
      (tester) async {
    final opened = <Uri>[];
    await _pump(tester, links, (uri) async {
      opened.add(uri);
      return true;
    });

    await tester.tap(find.widgetWithText(ActionChip, 'Open Library'));
    await tester.pumpAndSettle();

    expect(opened, [Uri.parse('https://openlibrary.org/works/OL262758W')]);
    expect(find.byType(SnackBar), findsNothing);
  });

  testWidgets('a page that could not open says so by name', (tester) async {
    await _pump(tester, links, (_) async => false);

    await tester.tap(find.widgetWithText(ActionChip, 'Goodreads'));
    await tester.pumpAndSettle();

    expect(find.text("Couldn't open Goodreads."), findsOneWidget);
  });

  testWidgets('a launcher that throws is reported the same way',
      (tester) async {
    await _pump(tester, links, (_) async => throw StateError('no handler'));

    await tester.tap(find.widgetWithText(ActionChip, 'Goodreads'));
    await tester.pumpAndSettle();

    expect(find.text("Couldn't open Goodreads."), findsOneWidget);
  });
}

Future<void> _pump(
  WidgetTester tester,
  List<TitleLink> links,
  BookLinkLauncher launch,
) async {
  await tester.pumpWidget(MaterialApp(
    home: Scaffold(
      body: Center(child: BookLinkChips(links, launch: launch)),
    ),
  ));
}
