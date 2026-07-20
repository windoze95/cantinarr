import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/config_changes/data/config_change_models.dart';
import 'package:cantinarr/features/config_changes/data/config_changes_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses the backend field projection and live detail state', () {
    final change = ConfigChange.fromJson(_changeJson());

    expect(change.id, 42);
    expect(change.source, ConfigChangeSource.aiChat);
    expect(change.operation, ConfigChangeOperation.update);
    expect(change.status, ConfigChangeStatus.applied);
    expect(change.currentStatus, ConfigCurrentStatus.matchesApplied);
    expect(change.canRevert, isTrue);
    expect(change.changes, hasLength(3));
    expect(change.changes[0].before, '0');
    expect(change.changes[0].after, '+100');
    expect(
      change.changes[0].currentStateLabelFor(change.recordedValueLabel),
      'Matches applied',
    );
    expect(change.changes[1].after, 'On');
    expect(change.changes[2].after, 'Preferred');
    expect(change.changes[0].hasCurrent, isTrue);
  });

  test('unknown sources remain readable for forward compatibility', () {
    final change = ConfigChange.fromJson({
      ..._changeJson(),
      'source': 'future_automation',
    });

    expect(change.source, ConfigChangeSource.unknown);
    expect(change.sourceLabel, 'Future Automation');
  });

  test('models current system-source custom-format creation records', () {
    final change = ConfigChange.fromJson({
      ..._changeJson(),
      'source': 'system',
      'resource_type': 'custom_format',
      'operation': 'create',
      'can_revert': false,
    });

    expect(change.source, ConfigChangeSource.system);
    expect(change.sourceLabel, 'System');
    expect(change.operation, ConfigChangeOperation.create);
    expect(change.canRevert, isFalse);
  });

  test('uses status-aware language for the recorded after projection', () {
    final applied = ConfigChange.fromJson(_changeJson());
    final failed = ConfigChange.fromJson({
      ..._changeJson(),
      'status': 'failed',
    });
    final unresolved = ConfigChange.fromJson({
      ..._changeJson(),
      'status': 'outcome_unknown',
    });
    final executing = ConfigChange.fromJson({
      ..._changeJson(),
      'status': 'executing',
    });
    final unknown = ConfigChange.fromJson({
      ..._changeJson(),
      'status': 'future_status',
    });

    expect(applied.recordedValueLabel, 'Applied');
    expect(applied.comparisonTitle, 'Before, applied, and current');
    expect(failed.recordedValueLabel, 'Attempted');
    expect(failed.comparisonTitle, 'Before, attempted, and current');
    expect(failed.recordedValueMatchLabel, 'Matches attempted');
    expect(
      failed.changes[0].currentStateLabelFor(failed.recordedValueLabel),
      'Matches attempted',
    );
    expect(unresolved.recordedValueLabel, 'Attempted');
    expect(executing.recordedValueLabel, 'Intended');
    expect(executing.comparisonTitle, 'Before, intended, and current');
    expect(
      executing.changes[0]
          .currentStateLabelFor(executing.recordedValueLabel),
      'Matches intended',
    );
    expect(unknown.recordedValueLabel, 'Intended');
  });

  test('service uses list, detail, and server-owned revert endpoints', () async {
    final adapter = _ConfigChangesAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final service = ConfigChangesService(backendDio: dio);

    final listed = await service.listChanges();
    final detail = await service.getChange(42);
    final restored = await service.revertChange(42);
    await service.listChanges(limit: 25, beforeId: 17);

    expect(listed.single.id, 42);
    expect(listed.single.canRevert, isFalse);
    expect(listed.single.currentStatus, isNull);
    expect(listed.single.changes, isEmpty);
    expect(detail.currentStatus, ConfigCurrentStatus.matchesApplied);
    expect(restored.operation, ConfigChangeOperation.revert);
    expect(adapter.requests, [
      ('GET', '/api/admin/external-settings-changes'),
      ('GET', '/api/admin/external-settings-changes/42'),
      ('POST', '/api/admin/external-settings-changes/42/revert'),
      ('GET', '/api/admin/external-settings-changes'),
    ]);
    expect(adapter.queries.first, {'limit': '100'});
    expect(adapter.queries.last, {'limit': '25', 'before_id': '17'});
  });
}

Map<String, dynamic> _changeJson() => {
      'id': 42,
      'actor_user_id': 1,
      'actor_name': 'Alex',
      'source': 'ai_chat',
      'service_type': 'sonarr',
      'instance_id': 'sonarr-main',
      'instance_name': 'Main Sonarr',
      'resource_type': 'quality_profile',
      'resource_id': '7',
      'resource_name': 'Very High (4K)',
      'operation': 'update',
      'status': 'applied',
      'summary': 'Prefer English releases',
      'changes': [
        {
          'key': 'english_score',
          'label': 'English',
          'before': '0',
          'after': '+100',
          'current': '+100',
          'current_state': 'matches_applied',
        },
        {
          'key': 'enabled',
          'label': 'Enabled',
          'before': 'Off',
          'after': 'On',
          'current': 'On',
        },
        {
          'key': 'options',
          'label': 'Options',
          'before': 'Not preferred',
          'after': 'Preferred',
          'current': 'Preferred',
        },
      ],
      'created_at': '2026-07-20T21:57:00Z',
      'completed_at': '2026-07-20T21:57:02Z',
      'current_status': 'matches_applied',
      'can_revert': true,
    };

Map<String, dynamic> _summaryChangeJson() {
  final summary = Map<String, dynamic>.from(_changeJson());
  summary.remove('current_status');
  summary['can_revert'] = false;
  summary['changes'] = const [];
  return summary;
}

class _ConfigChangesAdapter implements HttpClientAdapter {
  final requests = <(String, String)>[];
  final queries = <Map<String, String>>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add((options.method, options.uri.path));
    queries.add(options.uri.queryParameters);
    final change = _changeJson();
    final body = options.uri.path == '/api/admin/external-settings-changes'
        ? {'changes': [_summaryChangeJson()]}
        : options.uri.path.endsWith('/revert')
            ? {
                ...change,
                'id': 43,
                'parent_id': 42,
                'source': 'admin_revert',
                'operation': 'revert',
                'summary': 'Restored previous settings',
              }
            : change;
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
