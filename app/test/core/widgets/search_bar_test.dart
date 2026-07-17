import 'package:cantinarr/core/widgets/search_bar.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('exposes a stable semantics identifier', (tester) async {
    final semantics = tester.ensureSemantics();
    final controller = TextEditingController();
    final focusNode = FocusNode();

    addTearDown(controller.dispose);
    addTearDown(focusNode.dispose);

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: CantinarrSearchBar(
            controller: controller,
            focusNode: focusNode,
          ),
        ),
      ),
    );

    expect(find.bySemanticsIdentifier('global-search'), findsOneWidget);
    semantics.dispose();
  });

  testWidgets('multiline search bar submits on keyboard send action',
      (tester) async {
    final controller = TextEditingController(text: 'Find something good');
    final focusNode = FocusNode();
    var sendCount = 0;

    addTearDown(controller.dispose);
    addTearDown(focusNode.dispose);

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: CantinarrSearchBar(
            controller: controller,
            focusNode: focusNode,
            multiline: true,
            onSend: () => sendCount++,
          ),
        ),
      ),
    );

    await tester.tap(find.byType(TextField));
    await tester.pump();
    await tester.testTextInput.receiveAction(TextInputAction.send);
    await tester.pump();

    expect(sendCount, 1);
  });
}
