import 'package:cantinarr/features/issues/data/agent_action_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('AgentActionKind / AgentActionStatus', () {
    test('map wire values, tolerate unknowns', () {
      expect(AgentActionKind.fromValue('grab_release'),
          AgentActionKind.grabRelease);
      expect(AgentActionKind.fromValue('delete_media_files'),
          AgentActionKind.deleteMediaFiles);
      expect(AgentActionKind.deleteMediaFiles.value, 'delete_media_files');
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

    test('a stuck-upgrade abandon is a recognized queue fix', () {
      final a = AgentAction.fromJson({
        'id': 1,
        'issue_id': 2,
        'kind': 'remediate_queue',
        'params':
            '{"media_type":"movie","queue_id":7,"action":"blocklist_only"}',
        'rationale': '',
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
        'issue_media_type': 'movie',
        'instance_id': 'radarr-movies',
        'instance_name': 'Movies',
        'instance_service_type': 'radarr',
      });
      expect(a.params.queueAction, 'blocklist_only');
      expect(a.params.validationProblem(a.kind), isNull);
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

    test('S00 special search remains exact and actionable', () {
      final action = AgentAction.fromJson({
        'id': 33,
        'issue_id': 4,
        'kind': 'trigger_search',
        'params': {
          'media_type': 'tv',
          'tmdb_id': 42,
          'season': 0,
          'episode': 1,
        },
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
        'issue_media_type': 'tv',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });

      expect(action.params.season, 0);
      expect(action.params.episode, 1);
      expect(action.params.validationProblem(action.kind), isNull);
      expect(action.canTakeAction, isTrue);
    });

    test('a whole-season search is recognized and validated', () {
      final action = AgentAction.fromJson({
        'id': 36,
        'issue_id': 4,
        'kind': 'trigger_search',
        'params': {
          'media_type': 'tv',
          'tmdb_id': 615,
          'season': 11,
        },
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
        'issue_media_type': 'tv',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });

      expect(action.params.season, 11);
      expect(action.params.episode, isNull);
      expect(action.params.validationProblem(action.kind), isNull);
      expect(action.canTakeAction, isTrue);
    });

    test('a search still carrying aired_only is refused as unrecognized', () {
      // Replacing what a bad import destroyed is part of delete_media_files
      // now, not a search of its own, so the server cannot emit this field at
      // all. Anything that still does is stale or forged — it must fail the
      // allowlist, not be quietly accepted and then ignored. Every case below
      // is otherwise valid, so only the extra key can be doing the blocking.
      final cases = <Map<String, dynamic>>[
        {'media_type': 'tv', 'tmdb_id': 615, 'season': 11, 'aired_only': true},
        // A false flag is no more recognized than a true one.
        {'media_type': 'tv', 'tmdb_id': 615, 'season': 11, 'aired_only': false},
        {'media_type': 'movie', 'tmdb_id': 615, 'aired_only': true},
        {'media_type': 'book', 'book_id': 7, 'aired_only': true},
      ];

      for (final params in cases) {
        final action = AgentAction.fromJson({
          'id': 37,
          'issue_id': 4,
          'kind': 'trigger_search',
          'params': params,
          'status': 'proposed',
          'can_decide': true,
          'issue_status': 'awaiting_approval',
          'issue_media_type': 'tv',
          'instance_id': 'sonarr-living-room',
          'instance_name': 'Living Room TV',
          'instance_service_type': 'sonarr',
        });
        expect(
          action.params.validationProblem(action.kind),
          contains('does not recognize'),
          reason: '$params',
        );
        expect(action.canTakeAction, isFalse, reason: '$params');
      }
    });

    test('negative TV season is never actionable', () {
      final action = AgentAction.fromJson({
        'id': 34,
        'issue_id': 4,
        'kind': 'trigger_search',
        'params': {
          'media_type': 'tv',
          'tmdb_id': 42,
          'season': -1,
        },
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
        'issue_media_type': 'tv',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });

      expect(action.params.season, isNull);
      expect(action.params.validationProblem(action.kind), contains('season'));
      expect(action.canTakeAction, isFalse);
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

  group('delete_media_files', () {
    test('a TV deletion parses its exact target and stays actionable', () {
      final action = _delete({
        'media_type': 'tv',
        'tmdb_id': 615,
        'season': 11,
        'episodes': [1, 2, 3, 4, 5, 6, 7, 8, 9],
        'blocklist': true,
      });

      expect(action.kind, AgentActionKind.deleteMediaFiles);
      expect(action.params.mediaType, 'tv');
      expect(action.params.tmdbId, 615);
      expect(action.params.season, 11);
      expect(action.params.episodes, [1, 2, 3, 4, 5, 6, 7, 8, 9]);
      expect(action.params.blocklist, isTrue);
      expect(action.params.validationProblem(action.kind), isNull);
      expect(action.canTakeAction, isTrue);
    });

    test('a files-only deletion and an S00 deletion are both valid', () {
      final filesOnly = _delete({
        'media_type': 'tv',
        'tmdb_id': 615,
        'season': 11,
        'episodes': [3],
      });
      expect(filesOnly.params.blocklist, isFalse);
      expect(filesOnly.params.validationProblem(filesOnly.kind), isNull);
      expect(filesOnly.canTakeAction, isTrue);

      // Season 0 is Sonarr's specials; it must survive as an exact target.
      final specials = _delete({
        'media_type': 'tv',
        'tmdb_id': 615,
        'season': 0,
        'episodes': [1],
        'blocklist': true,
      });
      expect(specials.params.season, 0);
      expect(specials.params.validationProblem(specials.kind), isNull);
      expect(specials.canTakeAction, isTrue);
    });

    test('a movie deletion carries no episode scope', () {
      final action = _delete(
        {'media_type': 'movie', 'tmdb_id': 27205, 'blocklist': true},
        issueMediaType: 'movie',
        serviceType: 'radarr',
      );

      expect(action.params.episodes, isEmpty);
      expect(action.params.season, isNull);
      expect(action.params.blocklist, isTrue);
      expect(action.params.validationProblem(action.kind), isNull);
      expect(action.canTakeAction, isTrue);
    });

    test('a book deletion is addressed by the record id and stays actionable',
        () {
      // The wrong-book repair: the server proposes {media_type, book_id,
      // blocklist} and nothing else, and the app must let an admin approve it.
      final action = _delete(
        {'media_type': 'book', 'book_id': 912, 'blocklist': true},
        issueMediaType: 'book',
        serviceType: 'chaptarr',
      );

      expect(action.params.bookId, 912);
      expect(action.params.blocklist, isTrue);
      expect(action.params.validationProblem(action.kind), isNull);
      expect(action.canTakeAction, isTrue);

      // Blocklist stays optional, exactly like the video deletions.
      final filesOnly = _delete(
        {'media_type': 'book', 'book_id': 912},
        issueMediaType: 'book',
        serviceType: 'chaptarr',
      );
      expect(filesOnly.params.blocklist, isFalse);
      expect(filesOnly.params.validationProblem(filesOnly.kind), isNull);
      expect(filesOnly.canTakeAction, isTrue);
    });

    test('a music deletion is addressed by the album id and stays actionable',
        () {
      // The wrong-album repair: {media_type, album_id, blocklist} and nothing
      // else, riding the same approval card as books.
      final action = _delete(
        {'media_type': 'music', 'album_id': 314, 'blocklist': true},
        issueMediaType: 'music',
        serviceType: 'lidarr',
      );
      expect(action.params.albumId, 314);
      expect(action.params.validationProblem(action.kind), isNull);
      expect(action.canTakeAction, isTrue);
    });

    test('every malformed or under-specified deletion is refused', () {
      // (params, expected message fragment). One rejection path per row; each
      // row is otherwise valid so the fragment proves which gate fired.
      final cases = <(Map<String, dynamic>, String)>[
        // A book delete is addressed by the durable record id alone.
        ({'media_type': 'book'}, 'book whose files'),
        ({'media_type': 'book', 'book_id': 0}, 'book whose files'),
        // A numeric-looking string is never coerced into an identifier.
        ({'media_type': 'book', 'book_id': '912'}, 'book whose files'),
        // …and takes only book_id and blocklist; video scope is a category
        // error, exactly as the server refuses it.
        (
          {'media_type': 'book', 'book_id': 912, 'tmdb_id': 27205},
          'invalid media details'
        ),
        (
          {'media_type': 'book', 'book_id': 912, 'season': 1},
          'invalid media details'
        ),
        // The converse: a video deletion must not carry a book id.
        (
          {'media_type': 'movie', 'tmdb_id': 27205, 'book_id': 912},
          'book details'
        ),
        // The same fences for music: an album delete takes only its id, and a
        // video deletion never carries one.
        ({'media_type': 'music'}, 'album whose files'),
        (
          {'media_type': 'music', 'album_id': 314, 'tmdb_id': 5},
          'invalid media details'
        ),
        (
          {'media_type': 'music', 'album_id': 314, 'book_id': 9},
          'invalid media details'
        ),
        (
          {'media_type': 'movie', 'tmdb_id': 27205, 'album_id': 314},
          'music details'
        ),
        // No title to resolve the library record from.
        ({'media_type': 'movie'}, 'title'),
        ({'media_type': 'movie', 'tmdb_id': 0}, 'title'),
        // A numeric-looking string is never coerced into an identifier.
        ({'media_type': 'movie', 'tmdb_id': '27205'}, 'title'),
        // The blocklist choice decides a whole extra consequence: it is a bool
        // or the proposal is unreadable.
        (
          {'media_type': 'movie', 'tmdb_id': 27205, 'blocklist': 'yes'},
          'options are malformed'
        ),
        // Movies address one library file; episode scope is a category error.
        (
          {'media_type': 'movie', 'tmdb_id': 27205, 'season': 1},
          'TV episode details'
        ),
        (
          {'media_type': 'movie', 'tmdb_id': 27205, 'episodes': [1]},
          'TV episode details'
        ),
        // TV requires an exact season…
        ({'media_type': 'tv', 'tmdb_id': 615, 'episodes': [1]}, 'season'),
        (
          {
            'media_type': 'tv',
            'tmdb_id': 615,
            'season': -1,
            'episodes': [1],
          },
          'season'
        ),
        (
          {
            'media_type': 'tv',
            'tmdb_id': 615,
            'season': '11',
            'episodes': [1],
          },
          'season'
        ),
        // …and a well-typed, non-empty list of episode numbers.
        (
          {'media_type': 'tv', 'tmdb_id': 615, 'season': 11, 'episodes': 3},
          'malformed'
        ),
        (
          {
            'media_type': 'tv',
            'tmdb_id': 615,
            'season': 11,
            'episodes': [1, '2'],
          },
          'malformed'
        ),
        (
          {
            'media_type': 'tv',
            'tmdb_id': 615,
            'season': 11,
            'episodes': [1.5],
          },
          'malformed'
        ),
        (
          {
            'media_type': 'tv',
            'tmdb_id': 615,
            'season': 11,
            'episodes': <int>[],
          },
          'missing'
        ),
        ({'media_type': 'tv', 'tmdb_id': 615, 'season': 11}, 'missing'),
        (
          {
            'media_type': 'tv',
            'tmdb_id': 615,
            'season': 11,
            'episodes': [0, 1],
          },
          'episode numbers are invalid'
        ),
        (
          {
            'media_type': 'tv',
            'tmdb_id': 615,
            'season': 11,
            'episodes': [1, -2],
          },
          'episode numbers are invalid'
        ),
        // Past the server's own per-proposal bound the card stops being
        // something an admin can read before authorising a deletion.
        (
          {
            'media_type': 'tv',
            'tmdb_id': 615,
            'season': 11,
            'episodes': List<int>.generate(61, (i) => i + 1),
          },
          'too many episodes'
        ),
        // A field this app does not model could carry an unreviewed effect.
        (
          {
            'media_type': 'tv',
            'tmdb_id': 615,
            'season': 11,
            'episodes': [1],
            'delete_series': true,
          },
          'does not recognize'
        ),
      ];

      for (final (params, fragment) in cases) {
        final action = _delete(params);
        expect(
          action.params.validationProblem(action.kind),
          contains(fragment),
          reason: '$params',
        );
        expect(action.canTakeAction, isFalse, reason: '$params');
      }
    });

    test('60 episodes is still reviewable; the bound is exclusive', () {
      final action = _delete({
        'media_type': 'tv',
        'tmdb_id': 615,
        'season': 11,
        'episodes': List<int>.generate(60, (i) => i + 1),
      });
      expect(action.params.validationProblem(action.kind), isNull);
      expect(action.canTakeAction, isTrue);
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

    test('recovery-aborted runs do not claim the issue closed', () {
      for (final entry in {
        'arr_recovery_in_flight': 'resumed download recovery',
        'media_state_changed': 'live media state changed',
        'recovery_preflight_failed': 'could not be verified',
      }.entries) {
        final run = AgentRun.fromJson({
          'id': 1,
          'issue_id': 2,
          'trigger': 'user_report',
          'status': 'aborted',
          'stop_reason': entry.key,
        });

        expect(run.statusLabel, 'Investigation stopped');
        expect(run.stopReasonLabel, contains(entry.value));
        expect(run.statusLabel, isNot(contains('closed')));
      }
    });

    test('parses a run + ordered steps without exposing provider cost', () {
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

  group('standing-rule fields', () {
    test('offer and attribution parse from the server shape', () {
      final action = AgentAction.fromJson({
        'id': 1,
        'issue_id': 2,
        'kind': 'manual_import',
        'params': {'media_type': 'movie', 'queue_id': 7},
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
        'auto_approval_offer': {
          'problem_kind': 'Waiting to import',
          'action_kind': 'manual_import',
          'action_facet': '',
          'label': 'Manual import · Waiting to import',
          'reactivates_paused_rule': true,
        },
      });
      final offer = action.autoApprovalOffer;
      expect(offer, isNotNull);
      expect(offer!.label, 'Manual import · Waiting to import');
      expect(offer.problemKind, 'Waiting to import');
      expect(offer.reactivatesPausedRule, isTrue);
      expect(action.autoApproved, isFalse);

      final decided = AgentAction.fromJson({
        'id': 3,
        'issue_id': 2,
        'kind': 'manual_import',
        'params': {'media_type': 'movie', 'queue_id': 7},
        'status': 'executed',
        'auto_rule_id': 9,
        'auto_approved': true,
        'auto_rule_label': 'Manual import · Waiting to import',
      });
      expect(decided.autoApproved, isTrue);
      expect(decided.autoRuleId, 9);
      expect(decided.autoRuleLabel, 'Manual import · Waiting to import');
      expect(decided.decidedBy, isNull);
    });

    test('old servers without the fields parse to null/false', () {
      final action = AgentAction.fromJson({
        'id': 1,
        'issue_id': 2,
        'kind': 'manual_import',
        'params': {'media_type': 'movie', 'queue_id': 7},
        'status': 'proposed',
        'can_decide': true,
        'issue_status': 'awaiting_approval',
      });
      expect(action.autoApprovalOffer, isNull);
      expect(action.autoApproved, isFalse);
      expect(action.autoRuleId, isNull);
      expect(action.autoRuleLabel, isNull);
    });

    test('a malformed or empty offer never becomes an offer object', () {
      for (final bad in [
        'not-an-object',
        <String, dynamic>{},
        {'label': ''},
      ]) {
        final action = AgentAction.fromJson({
          'id': 1,
          'issue_id': 2,
          'kind': 'manual_import',
          'params': {'media_type': 'movie', 'queue_id': 7},
          'status': 'proposed',
          'can_decide': true,
          'issue_status': 'awaiting_approval',
          'auto_approval_offer': bad,
        });
        expect(action.autoApprovalOffer, isNull, reason: '$bad');
      }
    });

    test('the offer never changes the decision gate', () {
      // Same blocked action with and without an offer: the gate answer is
      // identical, so display data can never loosen safety.
      final base = {
        'id': 1,
        'issue_id': 2,
        'kind': 'manual_import',
        'params': {'media_type': 'movie', 'queue_id': 7},
        'status': 'proposed',
        'can_decide': false,
        'blocked_reason': 'server said no',
        'issue_status': 'awaiting_approval',
      };
      final withOffer = AgentAction.fromJson({
        ...base,
        'auto_approval_offer': {
          'problem_kind': 'Waiting to import',
          'action_kind': 'manual_import',
          'action_facet': '',
          'label': 'Manual import · Waiting to import',
          'reactivates_paused_rule': false,
        },
      });
      final withoutOffer = AgentAction.fromJson(base);
      expect(
        withOffer.decisionBlockedReason,
        withoutOffer.decisionBlockedReason,
      );
      expect(withOffer.canTakeAction, isFalse);
    });
  });

  group('AgentActionBatchResult', () {
    test('parses the wire shape and buckets each verdict', () {
      final executed = AgentActionBatchResult.fromJson(
          {'id': 7, 'status': 'executed'});
      expect(executed.id, 7);
      expect(executed.applied, isTrue);
      expect(executed.skipped, isFalse);
      expect(executed.needsAttention, isFalse);
      expect(executed.detail, '');

      final skipped = AgentActionBatchResult.fromJson({
        'id': 8,
        'status': 'skipped',
        'detail': 'the arr began recovering before dispatch',
      });
      expect(skipped.skipped, isTrue);
      expect(skipped.needsAttention, isFalse);
      expect(skipped.detail, contains('recovering'));

      // superseded/executing: owned elsewhere, nothing for the admin to do.
      expect(_r('superseded').skipped, isTrue);
      expect(_r('executing').skipped, isTrue);

      // failed / outcome_unknown / error / anything novel: surface it.
      expect(_r('failed').needsAttention, isTrue);
      expect(_r('outcome_unknown').needsAttention, isTrue);
      expect(_r('error').needsAttention, isTrue);
      expect(_r('').needsAttention, isTrue);
    });
  });
}

AgentActionBatchResult _r(String status) =>
    AgentActionBatchResult.fromJson({'id': 1, 'status': status});

/// A proposed `delete_media_files` action carrying [params] verbatim, on an
/// otherwise decidable issue so params validation is the only thing under test.
AgentAction _delete(
  Map<String, dynamic> params, {
  String issueMediaType = 'tv',
  String serviceType = 'sonarr',
}) =>
    AgentAction.fromJson({
      'id': 40,
      'issue_id': 6,
      'kind': 'delete_media_files',
      'params': params,
      'rationale': 'Nine files were imported before those episodes aired.',
      'status': 'proposed',
      'can_decide': true,
      'issue_status': 'awaiting_approval',
      'issue_media_type': issueMediaType,
      'instance_id': '$serviceType-main',
      'instance_name': switch (serviceType) {
        'radarr' => 'Main Movies',
        'chaptarr' => 'Main Books',
        'lidarr' => 'Main Music',
        _ => 'Main TV',
      },
      'instance_service_type': serviceType,
    });
