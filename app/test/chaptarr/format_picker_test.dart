import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/widgets/format_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

ChaptarrBook _book({
  required int id,
  required String mediaType,
  bool monitored = true,
  bool hasFile = false,
}) =>
    ChaptarrBook(
      id: id,
      title: 'Flock',
      mediaType: mediaType,
      monitored: monitored,
      statistics: ChaptarrBookStatistics(
        bookCount: 1,
        bookFileCount: hasFile ? 1 : 0,
      ),
    );

void main() {
  testWidgets('automatic picker groups duplicate records by format',
      (tester) async {
    List<ChaptarrBook>? selected;
    final records = [
      _book(id: 11, mediaType: 'ebook'),
      _book(id: 12, mediaType: 'ebook', monitored: false),
      _book(id: 21, mediaType: 'audiobook'),
    ];

    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: Builder(builder: (context) {
          return TextButton(
            onPressed: () async {
              selected = await pickFormatRecords(context, records);
            },
            child: const Text('Pick automatic'),
          );
        }),
      ),
    ));

    await tester.tap(find.text('Pick automatic'));
    await tester.pumpAndSettle();

    expect(find.text('eBook'), findsOneWidget);
    expect(find.text('Audiobook'), findsOneWidget);
    expect(find.text('2 records'), findsOneWidget);

    await tester.tap(find.text('eBook'));
    await tester.pumpAndSettle();

    expect(selected!.map((record) => record.id), [11, 12]);
  });

  testWidgets('interactive picker distinguishes same-format records by ID',
      (tester) async {
    ChaptarrBook? selected;
    final records = [
      _book(id: 11, mediaType: 'ebook', hasFile: true),
      _book(id: 12, mediaType: 'ebook', monitored: false),
      _book(id: 21, mediaType: 'audiobook'),
    ];

    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: Builder(builder: (context) {
          return TextButton(
            onPressed: () async {
              selected = await pickInteractiveFormatRecord(context, records);
            },
            child: const Text('Pick interactive'),
          );
        }),
      ),
    ));

    await tester.tap(find.text('Pick interactive'));
    await tester.pumpAndSettle();
    expect(find.text('eBook'), findsOneWidget);
    expect(find.text('Audiobook'), findsOneWidget);

    await tester.tap(find.text('eBook'));
    await tester.pumpAndSettle();

    expect(find.text('Which record?'), findsOneWidget);
    expect(find.text('Record #11'), findsOneWidget);
    expect(find.text('Record #12'), findsOneWidget);
    expect(find.textContaining('Downloaded'), findsOneWidget);
    expect(find.textContaining('Not requested'), findsOneWidget);

    await tester.tap(find.text('Record #12'));
    await tester.pumpAndSettle();

    expect(selected?.id, 12);
  });
}
