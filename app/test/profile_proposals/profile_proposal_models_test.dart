import 'package:cantinarr/features/profile_proposals/data/profile_proposal_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('ProfileChangeProposal', () {
    test('fromJson reads the wire shape', () {
      final proposal = ProfileChangeProposal.fromJson(const {
        'id': 12,
        'status': 'pending',
        'service': 'radarr',
        'instance_id': 'movies-a',
        'instance_name': 'Movies',
        'profile_id': 6,
        'profile_name': 'HD-1080p',
        'proposed_by_name': 'julian',
        'source_client': 'MCP: Claude Desktop',
        'diff': ['upgrade policy: on -> off'],
        'created_at': '2026-08-08T20:00:00Z',
        'expires_at': '2026-08-15T20:00:00Z',
      });
      expect(proposal.id, 12);
      expect(proposal.isPending, isTrue);
      expect(proposal.profileName, 'HD-1080p');
      expect(proposal.sourceClient, 'MCP: Claude Desktop');
      expect(proposal.diff, ['upgrade policy: on -> off']);
      expect(proposal.createdAt, isNotNull);
      expect(proposal.statusLabel, 'Awaiting approval');
    });

    test('tolerates missing optional fields', () {
      final proposal = ProfileChangeProposal.fromJson(const {
        'id': 3,
        'status': 'rejected',
        'service': 'sonarr',
        'instance_id': 'tv-a',
        'profile_id': 1,
      });
      expect(proposal.isPending, isFalse);
      expect(proposal.diff, isEmpty);
      expect(proposal.createdAt, isNull);
      expect(proposal.statusLabel, 'Rejected');
    });

    test('status labels stay requester-readable', () {
      const labels = {
        'applied': 'Applied',
        'superseded': 'Replaced by a newer proposal',
        'expired': 'Expired',
        'failed': 'Not applied',
      };
      labels.forEach((status, label) {
        final proposal = ProfileChangeProposal.fromJson({
          'id': 1,
          'status': status,
          'service': 'radarr',
          'instance_id': 'a',
          'profile_id': 1,
        });
        expect(proposal.statusLabel, label, reason: status);
      });
    });
  });
}
