import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import 'profile_proposal_models.dart';

/// Admin-only REST client for profile changes parked by external MCP agents.
/// Approval executes server-side against re-validated live settings; the
/// device never sends plans or values back, only the decision.
class ProfileProposalsService {
  final Dio _dio;

  ProfileProposalsService({required Dio backendDio}) : _dio = backendDio;

  Future<List<ProfileChangeProposal>> listProposals({
    String status = 'all',
  }) async {
    final response = await _dio.get(
      '/api/admin/profile-change-proposals',
      queryParameters: {if (status.isNotEmpty) 'status': status},
    );
    final data = response.data as Map<String, dynamic>? ?? const {};
    return ((data['proposals'] as List?) ?? const [])
        .whereType<Map>()
        .map((item) => ProfileChangeProposal.fromJson(
              item.map((key, value) => MapEntry(key.toString(), value)),
            ))
        .toList(growable: false);
  }

  Future<ProfileChangeProposal> getProposal(int id) async {
    final response = await _dio.get('/api/admin/profile-change-proposals/$id');
    return ProfileChangeProposal.fromJson(
        (response.data as Map<String, dynamic>? ?? const {}));
  }

  Future<ProfileChangeProposal> approveProposal(int id) async {
    final response =
        await _dio.post('/api/admin/profile-change-proposals/$id/approve');
    return ProfileChangeProposal.fromJson(
        (response.data as Map<String, dynamic>? ?? const {}));
  }

  Future<ProfileChangeProposal> rejectProposal(int id, {String? note}) async {
    final trimmed = note?.trim();
    final response = await _dio.post(
      '/api/admin/profile-change-proposals/$id/reject',
      data: {'note': trimmed ?? ''},
    );
    return ProfileChangeProposal.fromJson(
        (response.data as Map<String, dynamic>? ?? const {}));
  }
}

final profileProposalsServiceProvider = Provider<ProfileProposalsService>(
  (ref) =>
      ProfileProposalsService(backendDio: ref.watch(backendClientProvider)),
);
