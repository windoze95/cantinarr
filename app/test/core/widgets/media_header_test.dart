import 'package:cantinarr/core/widgets/media_header.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('media header shows the title without generic eyebrow copy',
      (tester) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(
          body: MediaHeader(title: 'Project Hail Mary'),
        ),
      ),
    );

    expect(find.text('Project Hail Mary'), findsOneWidget);
    expect(find.text('NOW IN FOCUS'), findsNothing);
  });
}
