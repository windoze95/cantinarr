import 'package:cantinarr/features/issues/data/agent_action_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('AgentActionKind / AgentActionStatus', () {
    test('map wire values, tolerate unknowns', () {
      expect(AgentActionKind.fromValue('grab_release'),
          AgentActionKind.grabRelease);
      expect(
          AgentActionKind.fromValue('a_future_kind'), AgentActionKind.unknown);
      expect(AgentActionKind.fromValue(null), AgentActionKind.unknown);

      expect(
          AgentActionStatus.fromValue('proposed'), AgentActionStatus.proposed);
      expect(AgentActionStatus.fromValue('something_new'),
          AgentActionStatus.unknown);
    });

    test('isPending only for proposed; isDecided for terminal decisions', () {
      expect(AgentActionStatus.proposed.isPending, isTrue);
      expect(AgentActionStatus.executing.isPending, isFalse);
      expect(AgentActionStatus.denied.isPending, isFalse);

      expect(AgentActionStatus.executed.isDecided, isTrue);
      expect(AgentActionStatus.denied.isDecided, isTrue);
      expect(AgentActionStatus.failed.isDecided, isTrue);
      expect(AgentActionStatus.proposed.isDecided, isFalse);
    });
  });

  group('AgentAction.fromJson', () {
    test('parses the merged server contract incl. joined issue fields', () {
      final a = AgentAction.fromJson({
        'id': 12,
        'issue_id': 5,
        'run_id': 9,
        'kind': 'grab_release',
        'params': {
          'media_type': 'tv',
          'guid': 'abc-123-def',
          'indexer_id': 2,
          'queue_id_to_replace': 44,
          'release_title': 'The.Show.S02E04.1080p.WEB',
          'quality': 'WEBDL-1080p',
          'size': 2147483648,
          'protocol': 'usenet',
          'indexer': 'Example Indexer',
        },
        'rationale': 'The current release is dual-audio; this one is English.',
        'risk': 'mutating',
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
        'decided_by': null,
        'decided_at': null,
        'deny_reason': null,
        'executed_at': null,
        'result_text': null,
        'created_at': '2026-06-23T10:00:00Z',
        'issue_title': 'The Show',
        'issue_media_type': 'tv',
        'issue_category': 'wrong_audio',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });

      expect(a.id, 12);
      expect(a.issueId, 5);
      expect(a.runId, 9);
      expect(a.kind, AgentActionKind.grabRelease);
      expect(a.status, AgentActionStatus.proposed);
      expect(a.rationale, contains('English'));
      expect(a.issueTitle, 'The Show');
      expect(a.issueCategory, 'wrong_audio');
      expect(a.instanceId, 'sonarr-living-room');
      expect(a.instanceName, 'Living Room TV');
      expect(a.instanceServiceType, 'sonarr');
      expect(a.instanceServiceLabel, 'Sonarr');
      expect(a.canTakeAction, isTrue);

      // Typed params view.
      expect(a.params.mediaType, 'tv');
      expect(a.params.guid, 'abc-123-def');
      expect(a.params.indexerId, 2);
      expect(a.params.queueIdToReplace, 44);
      expect(a.params.releaseTitle, 'The.Show.S02E04.1080p.WEB');
      expect(a.params.quality, 'WEBDL-1080p');
      expect(a.params.size, 2147483648);
      expect(a.params.protocol, 'usenet');
      expect(a.params.indexer, 'Example Indexer');
    });

    test('tolerates a stringified params object and a null run_id', () {
      final a = AgentAction.fromJson({
        'id': 1,
        'issue_id': 2,
        'run_id': null,
        'kind': 'remediate_queue',
        'params':
            '{"media_type":"movie","queue_id":7,"action":"blocklist_search"}',
        'rationale': '',
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
        'issue_media_type': 'tv',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });
      expect(a.runId, isNull);
      expect(a.params.mediaType, 'movie');
      expect(a.params.queueId, 7);
      expect(a.params.queueAction, 'blocklist_search');
    });

    test('malformed params never crash; an unknown kind still parses', () {
      final a = AgentAction.fromJson({
        'id': 1,
        'issue_id': 2,
        'kind': 'a_future_kind',
        'params': '{not valid json',
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
        'issue_media_type': 'tv',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });
      expect(a.kind, AgentActionKind.unknown);
      expect(a.kindRaw, 'a_future_kind');
      expect(a.params.isEmpty, isTrue);
      expect(a.canTakeAction, isFalse);
      expect(a.decisionBlockedReason, contains('recognize'));
    });

    test('decided action carries decision + result fields', () {
      final a = AgentAction.fromJson({
        'id': 3,
        'issue_id': 4,
        'kind': 'rescan',
        'params': {'media_type': 'movie', 'tmdb_id': 27205},
        'status': 'executed',
        'approved_params': {
          'media_type': 'movie',
          'tmdb_id': 27205,
        },
        'can_decide': false,
        'issue_status': 'resolved',
        'decided_by': 1,
        'decided_at': '2026-06-23T10:05:00Z',
        'executed_at': '2026-06-23T10:05:02Z',
        'result_text': 'Rescan triggered; import pass queued.',
      });
      expect(a.status, AgentActionStatus.executed);
      expect(a.decidedBy, 1);
      expect(a.decidedAt, isNotNull);
      expect(a.resultText, contains('Rescan'));
      expect(a.params.tmdbId, 27205);
      expect(a.approvedParams?.tmdbId, 27205);
    });

    test('episode-scoped trigger search is recognized and validated', () {
      final action = AgentAction.fromJson({
        'id': 31,
        'issue_id': 4,
        'kind': 'trigger_search',
        'params': {
          'media_type': 'tv',
          'tmdb_id': 42,
          'season': 2,
          'episode': 7,
        },
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
        'issue_media_type': 'tv',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });
      expect(action.params.season, 2);
      expect(action.params.episode, 7);
      expect(action.params.validationProblem(action.kind), isNull);
      expect(action.canTakeAction, isTrue);
    });

    test('missing or mismatched target metadata blocks a proposal', () {
      Map<String, dynamic> proposal() => {
            'id': 32,
            'issue_id': 4,
            'kind': 'rescan',
            'params': {'media_type': 'movie', 'tmdb_id': 42},
            'status': 'proposed',
            'can_decide': true,
            'issue_status': 'awaiting_approval',
            'issue_media_type': 'movie',
          };

      final missing = AgentAction.fromJson(proposal());
      expect(missing.canTakeAction, isFalse);
      expect(missing.decisionBlockedReason, contains('target instance'));

      final mismatchedJson = proposal()
        ..addAll({
          'instance_id': 'sonarr-main',
          'instance_name': 'Main TV',
          'instance_service_type': 'sonarr',
        });
      final mismatched = AgentAction.fromJson(mismatchedJson);
      expect(mismatched.canTakeAction, isFalse);
      expect(mismatched.decisionBlockedReason, contains('does not match'));
    });

    test('stale and malformed proposals are never actionable', () {
      final stale = AgentAction.fromJson({
        'id': 20,
        'issue_id': 4,
        'kind': 'rescan',
        'params': {'media_type': 'movie', 'tmdb_id': 42},
        'status': 'proposed',
        'can_decide': false,
        'blocked_reason': 'The issue closed before this fix was reviewed.',
        'issue_status': 'resolved',
        'issue_closed_at': '2026-07-10T12:00:00Z',
      });
      expect(stale.canTakeAction, isFalse);
      expect(stale.decisionBlockedReason, contains('closed'));

      final malformed = AgentAction.fromJson({
        'id': 21,
        'issue_id': 4,
        'kind': 'grab_release',
        'params': '{not-json',
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
      });
      expect(malformed.canTakeAction, isFalse);
      expect(malformed.decisionBlockedReason, contains('malformed'));

      final wrongTypes = AgentAction.fromJson({
        'id': 22,
        'issue_id': 4,
        'kind': 'grab_release',
        'params': {
          'media_type': 'movie',
          'guid': 'release',
          'indexer_id': '3',
        },
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
      });
      expect(wrongTypes.canTakeAction, isFalse);
      expect(wrongTypes.decisionBlockedReason, contains('details'));
    });
  });

  group('AgentRun / AgentStep / AgentRunDetail', () {
    test('labels an admin-completed run stop distinctly from dismissal', () {
      final completed = AgentRun.fromJson({
        'id': 10,
        'issue_id': 5,
        'status': 'aborted',
        'stop_reason': 'admin_completed',
      });
      expect(completed.stopReasonLabel, 'Completed after admin review');
    });

    test('parses a run + ordered steps with a cost label', () {
      final d = AgentRunDetail.fromJson({
        'run': {
          'id': 9,
          'issue_id': 5,
          'trigger': 'user_report',
          'status': 'succeeded',
          'model': 'claude-haiku-4-5',
          'step_count': 3,
          'input_tokens': 1200,
          'output_tokens': 300,
          'cache_creation_tokens': 0,
          'cache_read_tokens': 800,
          'cost_micros': 4200,
          'stop_reason': 'resolved',
          'started_at': '2026-06-23T10:00:00Z',
          'finished_at': '2026-06-23T10:00:30Z',
        },
        'steps': [
          {
            'id': 1,
            'seq': 0,
            'kind': 'tool_call',
            'tool_name': 'diagnose_queue',
            'tool_input': '{"media_type":"tv"}',
          },
          {
            'id': 2,
            'seq': 1,
            'kind': 'tool_result',
            'tool_name': 'diagnose_queue',
            'tool_output': 'stalled: no seeders',
            'is_error': false,
          },
          {
            'id': 3,
            'seq': 2,
            'kind': 'assistant',
            'text': 'Proposing a blocklist + search.',
          },
        ],
      });

      expect(d.run.id, 9);
      expect(d.run.model, 'claude-haiku-4-5');
      expect(d.run.cacheReadTokens, 800);
      expect(d.run.costLabel, '\$0.0042');
      expect(d.run.statusLabel, 'Investigation completed');
      expect(d.run.stopReasonLabel, 'Resolution verified');
      expect(d.steps, hasLength(3));
      expect(d.steps.first.toolName, 'diagnose_queue');
      expect(d.steps[1].kind, 'tool_result');
      expect(d.steps.last.text, contains('blocklist'));
    });

    test(
        'parses durable issue activity with terminal actions and run summaries',
        () {
      final activity = IssueAgentActivity.fromJson({
        'actions': [
          {
            'id': 3,
            'issue_id': 5,
            'kind': 'rescan',
            'params': {'media_type': 'movie', 'tmdb_id': 42},
            'status': 'outcome_unknown',
            'can_decide': false,
            'issue_status': 'resolved',
          },
        ],
        'runs': [
          {'id': 9, 'issue_id': 5, 'status': 'succeeded'},
        ],
      });
      expect(activity.actions.single.status, AgentActionStatus.outcomeUnknown);
      expect(activity.runs.single.id, 9);
    });
  });
}
