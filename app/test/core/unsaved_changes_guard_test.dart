import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:cantinarr/core/widgets/unsaved_changes_guard.dart';

void main() {
  test('snapshots detect mutable edits and become clean when reverted', () {
    final draft = SettingsDraft();
    final values = {
      'selected': <int>[1],
      'enabled': true
    };
    expect(draft.hasChanges(values), isFalse);
    draft.markSaved(values);
    (values['selected'] as List<int>).add(2);
    expect(draft.hasChanges(values), isTrue);
    (values['selected'] as List<int>).remove(2);
    expect(draft.hasChanges(values), isFalse);
  });

  testWidgets('navigation can be cancelled without losing typed text',
      (tester) async {
    final router = await pumpEditor(tester);
    await tester.enterText(find.byType(TextField), 'draft');
    router.go('/away');
    await tester.pumpAndSettle();
    expect(find.text('Discard unsaved changes?'), findsOneWidget);
    await tester.tap(find.text('Keep editing'));
    await tester.pumpAndSettle();
    expect(find.text('draft'), findsOneWidget);
    expect(router.routerDelegate.currentConfiguration.uri.path, '/edit/1');
    router.go('/away');
    await tester.pumpAndSettle();
    await tester.tap(find.text('Discard changes'));
    await tester.pumpAndSettle();
    expect(find.text('Destination'), findsOneWidget);
  });

  testWidgets('reverted edits and successful saves leave without prompting',
      (tester) async {
    final router = await pumpEditor(tester);
    await tester.enterText(find.byType(TextField), 'draft');
    await tester.enterText(find.byType(TextField), 'saved');
    router.go('/away');
    await tester.pumpAndSettle();
    expect(find.text('Discard unsaved changes?'), findsNothing);
    router.go('/edit/1');
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'new value');
    await tester.tap(find.text('Save'));
    router.go('/away');
    await tester.pumpAndSettle();
    expect(find.text('Destination'), findsOneWidget);
  });

  testWidgets('native Back on a pushed editor preserves the pop result',
      (tester) async {
    final router = await pumpEditor(tester, initialLocation: '/away');
    final result = router.push<int>('/edit/1');
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'draft');
    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    await tester.tap(find.text('Keep editing'));
    await tester.pumpAndSettle();
    expect(find.text('draft'), findsOneWidget);
    router.pop(7);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Discard changes'));
    await tester.pumpAndSettle();
    expect(await result, 7);
    expect(find.text('Destination'), findsOneWidget);
  });

  testWidgets(
      'pushing a child preserves the draft and warns when it is later removed',
      (tester) async {
    final router = await pumpEditor(tester);
    await tester.enterText(find.byType(TextField), 'draft');
    router.push('/away');
    await tester.pumpAndSettle();
    expect(find.text('Discard unsaved changes?'), findsNothing);
    router.pop();
    await tester.pumpAndSettle();
    expect(find.text('draft'), findsOneWidget);
    router.go('/away');
    await tester.pumpAndSettle();
    expect(find.text('Discard unsaved changes?'), findsOneWidget);
    await tester.tap(find.text('Keep editing'));
    await tester.pumpAndSettle();
  });

  testWidgets(
      'switching records on the same route warns before replacing the editor',
      (tester) async {
    final router = await pumpEditor(tester);
    await tester.enterText(find.byType(TextField), 'draft');
    router.go('/edit/2');
    await tester.pumpAndSettle();
    expect(find.text('Discard unsaved changes?'), findsOneWidget);
    await tester.tap(find.text('Keep editing'));
    await tester.pumpAndSettle();
    expect(find.text('Editor 1'), findsOneWidget);
    expect(find.text('draft'), findsOneWidget);
  });

  testWidgets('dialog dismissal keeps edits unless discard is confirmed',
      (tester) async {
    await pumpEditor(tester);
    await tester.tap(find.text('Open dialog'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).last, 'dialog draft');
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Keep editing'));
    await tester.pumpAndSettle();
    expect(find.text('dialog draft'), findsOneWidget);
    await tester.tapAt(const Offset(5, 5));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Discard changes'));
    await tester.pumpAndSettle();
    expect(find.text('Dialog editor'), findsNothing);
  });
}

Future<GoRouter> pumpEditor(WidgetTester tester,
    {String initialLocation = '/edit/1'}) async {
  final container = ProviderContainer();
  final router = GoRouter(initialLocation: initialLocation, routes: [
    GoRoute(
      path: '/edit/:id',
      onExit: container.read(unsavedChangesProvider).confirmExit,
      builder: (_, state) => _Editor(
          key: ValueKey(state.uri.path), id: state.pathParameters['id']!),
    ),
    GoRoute(
        path: '/away',
        builder: (_, __) => const Scaffold(body: Text('Destination'))),
  ]);
  addTearDown(() {
    router.dispose();
    container.dispose();
  });
  await tester.pumpWidget(UncontrolledProviderScope(
    container: container,
    child: MaterialApp.router(routerConfig: router),
  ));
  await tester.pumpAndSettle();
  return router;
}

class _Editor extends StatefulWidget {
  const _Editor({super.key, required this.id, this.dialog = false});
  final String id;
  final bool dialog;
  @override
  State<_Editor> createState() => _EditorState();
}

class _EditorState extends State<_Editor> {
  final text = TextEditingController(text: 'saved');
  final draft = SettingsDraft()..markSaved('saved');
  @override
  void dispose() {
    text.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => draft.hasChanges(text.text),
        isDialog: widget.dialog,
        child: widget.dialog
            ? AlertDialog(
                title: const Text('Dialog editor'),
                content: TextField(controller: text),
                actions: [
                  TextButton(
                      onPressed: () => Navigator.of(context).maybePop(),
                      child: const Text('Cancel')),
                ],
              )
            : Scaffold(
                appBar: AppBar(title: Text('Editor ${widget.id}')),
                body: Column(children: [
                  TextField(controller: text),
                  TextButton(
                      onPressed: () => draft.markSaved(text.text),
                      child: const Text('Save')),
                  TextButton(
                      onPressed: () => showDialog<void>(
                          context: context,
                          builder: (_) =>
                              const _Editor(id: 'dialog', dialog: true)),
                      child: const Text('Open dialog')),
                ])),
      );
}
