import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/widgets/instance_dropdown.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

const _main = ServiceInstance(
  id: 'radarr-main',
  serviceType: 'radarr',
  name: 'Main Radarr',
  isDefault: true,
);
const _fourK = ServiceInstance(
  id: 'radarr-4k',
  serviceType: 'radarr',
  name: '4K Radarr',
);

Future<void> _pump(
  WidgetTester tester, {
  required List<ServiceInstance> instances,
  String? activeInstanceId,
  ValueChanged<String>? onChanged,
}) {
  return tester.pumpWidget(MaterialApp(
    home: Scaffold(
      body: InstanceDropdown(
        instances: instances,
        activeInstanceId: activeInstanceId,
        onChanged: onChanged ?? (_) {},
      ),
    ),
  ));
}

void main() {
  testWidgets('a single instance renders nothing to switch', (tester) async {
    await _pump(
      tester,
      instances: const [_main],
      activeInstanceId: 'radarr-main',
    );

    expect(find.byType(DropdownButton<String>), findsNothing);
    expect(find.text('Main Radarr'), findsNothing);
  });

  testWidgets('multiple instances show only the active name collapsed',
      (tester) async {
    await _pump(
      tester,
      instances: const [_main, _fourK],
      activeInstanceId: 'radarr-main',
    );

    expect(find.byType(DropdownButton<String>), findsOneWidget);
    expect(find.text('Main Radarr'), findsOneWidget);
    expect(find.text('4K Radarr'), findsNothing);
  });

  testWidgets('selecting another instance reports its id', (tester) async {
    final changes = <String>[];
    await _pump(
      tester,
      instances: const [_main, _fourK],
      activeInstanceId: 'radarr-main',
      onChanged: changes.add,
    );

    await tester.tap(find.byType(DropdownButton<String>));
    await tester.pumpAndSettle();
    expect(find.text('4K Radarr'), findsOneWidget);

    await tester.tap(find.text('4K Radarr'));
    await tester.pumpAndSettle();

    expect(changes, ['radarr-4k']);
  });

  testWidgets('re-selecting the active instance still reports it',
      (tester) async {
    final changes = <String>[];
    await _pump(
      tester,
      instances: const [_main, _fourK],
      activeInstanceId: 'radarr-4k',
      onChanged: changes.add,
    );

    await tester.tap(find.byType(DropdownButton<String>));
    await tester.pumpAndSettle();
    // The open menu shows every choice, including the current one.
    expect(find.text('Main Radarr'), findsOneWidget);

    await tester.tap(find.text('4K Radarr').last);
    await tester.pumpAndSettle();

    expect(changes, ['radarr-4k']);
  });
}
