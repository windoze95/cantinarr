import 'package:cantinarr/features/settings/data/audiobook_library_access.dart';
import 'package:cantinarr/features/settings/data/instance_api_service.dart';
import 'package:cantinarr/features/settings/ui/audiobook_user_libraries.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final scale in [1.0, 2.0]) {
    testWidgets('individual library selection fits 320px at text scale $scale',
        (tester) async {
      tester.view.physicalSize = const Size(320, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      var policy =
          const AudiobookLibraryPolicy(mode: 'selected', libraryIds: ['yana']);
      await tester.pumpWidget(MaterialApp(
          home: MediaQuery(
              data: MediaQueryData(textScaler: TextScaler.linear(scale)),
              child: Scaffold(
                  body: SingleChildScrollView(
                      child: StatefulBuilder(
                builder: (context, setState) => AudiobookUserLibraries(
                    username: 'Yana',
                    policy: policy,
                    defaultLibraryIds: const {'books'},
                    libraries: const [
                      MediaServerLibrary(
                          id: 'books',
                          name: 'audiobooks',
                          collectionType: 'book'),
                      MediaServerLibrary(
                          id: 'yana',
                          name: 'audiobooks-yana',
                          collectionType: 'book')
                    ],
                    onChanged: (value) => setState(() => policy = value)),
              ))))));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      await tester.tap(find.widgetWithText(CheckboxListTile, 'audiobooks'));
      await tester.pumpAndSettle();
      expect(policy.libraryIds, ['books', 'yana']);
      await tester.tap(find.byType(DropdownButton<String>));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Use server default').last);
      await tester.pumpAndSettle();
      expect(policy.mode, 'default');
      expect(tester.takeException(), isNull);
    });
  }
}
