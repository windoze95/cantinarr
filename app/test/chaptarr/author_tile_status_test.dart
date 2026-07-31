import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_author_list.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Mirrors the Sonarr tile's grammar: the badge is the author's publishing
/// status, and the bar carries completeness — green only for an ended author
/// with every monitored book on disk, info/ember when merely caught up, red/amber
/// for monitored/unmonitored gaps.
ChaptarrAuthor _author({
  String? status,
  bool monitored = true,
  int files = 0,
  int count = 0,
}) =>
    ChaptarrAuthor(
      id: 1,
      authorName: 'Author',
      status: status,
      monitored: monitored,
      statistics:
          ChaptarrAuthorStatistics(bookFileCount: files, bookCount: count),
    );

Future<void> _pump(WidgetTester tester, ChaptarrAuthor author) {
  return tester.pumpWidget(MaterialApp(
    home: Scaffold(
      body: ChaptarrAuthorList(
        authors: [author],
        onTap: (_) {},
      ),
    ),
  ));
}

Color? _barColor(WidgetTester tester) {
  final bar = tester
      .widget<LinearProgressIndicator>(find.byType(LinearProgressIndicator));
  return bar.valueColor?.value;
}

void main() {
  testWidgets('caught-up continuing author stays Continuing with an info bar',
      (tester) async {
    await _pump(tester, _author(status: 'continuing', files: 12, count: 12));
    expect(find.text('Continuing'), findsOneWidget);
    expect(find.text('Complete'), findsNothing);
    expect(_barColor(tester), AppTheme.downloading);
  });

  testWidgets('complete ended author shows Ended with a green bar',
      (tester) async {
    await _pump(tester, _author(status: 'ended', files: 8, count: 8));
    expect(find.text('Ended'), findsOneWidget);
    expect(_barColor(tester), AppTheme.available);
  });

  testWidgets('monitored author with missing books shows a red bar',
      (tester) async {
    await _pump(tester, _author(status: 'continuing', files: 3, count: 9));
    expect(find.text('Continuing'), findsOneWidget);
    expect(_barColor(tester), AppTheme.error);
  });
}
