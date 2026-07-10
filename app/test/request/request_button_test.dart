import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/ui/request_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('available state is truthful and non-interactive',
      (tester) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(
          body: RequestButton(status: RequestStatus.available),
        ),
      ),
    );

    expect(find.text('Available'), findsOneWidget);
    expect(find.text('Watch Now'), findsNothing);

    final button = tester.widget<ElevatedButton>(find.byType(ElevatedButton));
    expect(button.onPressed, isNull);
  });
}
