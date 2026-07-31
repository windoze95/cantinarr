import 'dart:async';

import 'package:cantinarr/features/config_changes/data/config_change_models.dart';
import 'package:cantinarr/features/config_changes/data/config_changes_service.dart';
import 'package:cantinarr/features/config_changes/ui/config_change_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  testWidgets('a successful restore immediately reconciles to its history row',
      (tester) async {
    final applied = _change();
    final restored = _change(
      id: 43,
      parentId: 42,
      source: 'admin_revert',
      operation: 'revert',
      summary: 'Quality profile restore: "Example"',
      before: 1,
      after: 0,
      canRevert: false,
    );
    final service = _FakeConfigChangesService(
      applied: applied,
      restored: restored,
    );
    final router = GoRouter(
      initialLocation: '/settings/change-history/42',
      routes: [
        GoRoute(
          path: '/settings/change-history/:id',
          builder: (_, state) => ConfigChangeDetailScreen(
            changeId: int.parse(state.pathParameters['id']!),
          ),
        ),
      ],
    );
    addTearDown(() {
      if (!service.restoredDetail.isCompleted) {
        service.restoredDetail.complete(restored);
      }
      router.dispose();
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          configChangesServiceProvider.overrideWithValue(service),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    final restoreButton =
        find.widgetWithText(ElevatedButton, 'Restore previous settings');
    await tester.scrollUntilVisible(
      restoreButton,
      300,
      scrollable: _verticalScrollable(),
    );
    await tester.tap(restoreButton);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 500));
    expect(find.text('Restore previous settings?'), findsOneWidget);

    await tester.tap(restoreButton.last);
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/settings/change-history/43',
    );
    expect(find.text('Quality profile restore: "Example"'), findsOneWidget);
    expect(find.text('Restored'), findsOneWidget);
    final completedButton =
        find.widgetWithText(ElevatedButton, 'Previous settings restored');
    await tester.scrollUntilVisible(
      completedButton,
      300,
      scrollable: _verticalScrollable(),
    );
    expect(tester.widget<ElevatedButton>(completedButton).onPressed, isNull);
    expect(
      find.widgetWithText(ElevatedButton, 'Restore previous settings'),
      findsNothing,
    );
    expect(service.revertedIds, [42]);
    expect(service.loadedIds, [42, 42, 43]);

    service.restoredDetail.complete(restored);
    await tester.pumpAndSettle();
  });
}

Finder _verticalScrollable() => find
    .byWidgetPredicate(
      (widget) =>
          widget is Scrollable && widget.axisDirection == AxisDirection.down,
    )
    .first;

ConfigChange _change({
  int id = 42,
  int? parentId,
  String source = 'ai_chat',
  String operation = 'update',
  String summary = 'Quality profile update: "Example"',
  int before = 0,
  int after = 1,
  bool canRevert = true,
}) =>
    ConfigChange.fromJson({
      'id': id,
      if (parentId != null) 'parent_id': parentId,
      'actor_user_id': 1,
      'actor_name': 'Alex',
      'source': source,
      'service_type': 'sonarr',
      'instance_id': 'sonarr-main',
      'instance_name': 'Main Sonarr',
      'resource_type': 'quality_profile',
      'resource_id': '7',
      'resource_name': 'Example',
      'operation': operation,
      'status': 'applied',
      'summary': summary,
      'changes': [
        {
          'key': 'minimum_format_score',
          'label': 'Minimum custom-format score',
          'before': before,
          'after': after,
          'current': after,
          'current_state': 'matches_applied',
        },
      ],
      'created_at': '2026-07-20T21:57:00Z',
      'completed_at': '2026-07-20T21:57:02Z',
      'current_status': 'matches_applied',
      'can_revert': canRevert,
    });

class _FakeConfigChangesService extends ConfigChangesService {
  final ConfigChange applied;
  final ConfigChange restored;
  final restoredDetail = Completer<ConfigChange>();
  final loadedIds = <int>[];
  final revertedIds = <int>[];

  _FakeConfigChangesService({
    required this.applied,
    required this.restored,
  }) : super(backendDio: Dio());

  @override
  Future<ConfigChange> getChange(int id) {
    loadedIds.add(id);
    return switch (id) {
      42 => Future.value(applied),
      43 => restoredDetail.future,
      _ => Future.error(StateError('Unexpected change ID $id')),
    };
  }

  @override
  Future<ConfigChange> revertChange(int id) {
    revertedIds.add(id);
    return Future.value(restored);
  }
}
