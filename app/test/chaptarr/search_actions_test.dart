import 'package:cantinarr/features/chaptarr/ui/widgets/search_actions.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

Future<void> _pump(
  WidgetTester tester, {
  required Size size,
  double textScaleFactor = 1,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
  await tester.pumpWidget(MaterialApp(
    home: MediaQuery(
      data: MediaQueryData(
        size: size,
        textScaler: TextScaler.linear(textScaleFactor),
      ),
      child: Scaffold(
        body: Align(
          alignment: Alignment.bottomCenter,
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: ChaptarrSearchActions(
              onFindAutomatically: () {},
              onChooseDownload: () {},
            ),
          ),
        ),
      ),
    ),
  ));
}

void main() {
  testWidgets('uses one row at a normal phone width', (tester) async {
    await _pump(tester, size: const Size(390, 844));

    expect(find.text('Find automatically'), findsOneWidget);
    expect(find.text('Choose a download'), findsOneWidget);
    expect(
      tester.getTopLeft(find.text('Find automatically')).dy,
      tester.getTopLeft(find.text('Choose a download')).dy,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('stacks at narrow width without overflowing', (tester) async {
    await _pump(tester, size: const Size(320, 700));

    expect(
      tester.getTopLeft(find.text('Find automatically')).dy,
      lessThan(tester.getTopLeft(find.text('Choose a download')).dy),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('stacks for large text without overflowing', (tester) async {
    await _pump(
      tester,
      size: const Size(390, 844),
      textScaleFactor: 2,
    );

    expect(
      tester.getTopLeft(find.text('Find automatically')).dy,
      lessThan(tester.getTopLeft(find.text('Choose a download')).dy),
    );
    expect(tester.takeException(), isNull);
  });
}
