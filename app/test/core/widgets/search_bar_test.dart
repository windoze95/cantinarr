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

  testWidgets('empty send-mode field still offers a way out', (tester) async {
    final controller = TextEditingController();
    final focusNode = FocusNode();
    var cleared = 0;

    addTearDown(controller.dispose);
    addTearDown(focusNode.dispose);

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: CantinarrSearchBar(
            controller: controller,
            focusNode: focusNode,
            onSend: () {},
            onClear: () => cleared++,
          ),
        ),
      ),
    );

    await tester.tap(find.byTooltip('Exit AI mode'));
    expect(cleared, 1);
  });

  testWidgets('contextIcon outranks aiEnabled for the prefix glyph',
      (tester) async {
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
            aiEnabled: true,
            contextIcon: Icons.menu_book,
          ),
        ),
      ),
    );

    expect(find.byIcon(Icons.menu_book), findsOneWidget);
    expect(find.byIcon(Icons.auto_awesome_rounded), findsNothing);
  });

  testWidgets(
      'omitting contextIcon preserves the pre-phase aiEnabled default',
      (tester) async {
    final controllerAi = TextEditingController();
    final focusNodeAi = FocusNode();
    addTearDown(controllerAi.dispose);
    addTearDown(focusNodeAi.dispose);

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: CantinarrSearchBar(
            controller: controllerAi,
            focusNode: focusNodeAi,
            aiEnabled: true,
          ),
        ),
      ),
    );
    expect(find.byIcon(Icons.auto_awesome_rounded), findsOneWidget);

    final controllerPlain = TextEditingController();
    final focusNodePlain = FocusNode();
    addTearDown(controllerPlain.dispose);
    addTearDown(focusNodePlain.dispose);

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: CantinarrSearchBar(
            controller: controllerPlain,
            focusNode: focusNodePlain,
          ),
        ),
      ),
    );
    expect(find.byIcon(Icons.search_rounded), findsOneWidget);
  });
}
