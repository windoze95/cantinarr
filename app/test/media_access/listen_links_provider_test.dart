import 'dart:async';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/media_access/data/listen_links.dart';
import 'package:cantinarr/features/media_access/data/media_access_service.dart';
import 'package:cantinarr/features/media_access/logic/listen_links_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

AuthState _session(bool granted) => AuthState(
      user: const UserProfile(id: 1, username: 'reader', role: 'user'),
      connection: BackendConnection(
        serverUrl: 'https://cantinarr.example',
        accessToken: 'access',
        refreshToken: 'refresh',
        instances: [
          if (granted)
            const ServiceInstance(
                id: 'abs', serviceType: 'audiobookshelf', name: 'Books'),
        ],
      ),
    );

class _Auth extends AuthNotifier {
  @override
  Future<AuthState> build() async => _session(false);

  void grant(bool granted) => state = AsyncData(_session(granted));
}

class _Service extends MediaAccessService {
  _Service() : super(backendDio: Dio());
  final responses = <Completer<List<ListenLink>>>[];

  @override
  Future<List<ListenLink>> listenLinks(
      {required String instanceId, required String foreignBookId}) {
    expect(instanceId, 'chaptarr');
    expect(foreignBookId, 'hc:1');
    final next = Completer<List<ListenLink>>();
    responses.add(next);
    return next.future;
  }
}

void main() {
  test('grant removal discards an in-flight lookup; account changes refetch',
      () async {
    final auth = _Auth(), service = _Service();
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => auth),
      mediaAccessServiceProvider.overrideWithValue(service),
      libraryChangedEventsProvider.overrideWith((ref) => const Stream.empty()),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    final provider = listenLinksProvider(
        (instanceId: 'chaptarr', foreignId: 'hc:1', refreshTick: 0));
    final subscription = container.listen(provider, (before, after) {});
    addTearDown(subscription.close);
    expect(await container.read(provider.future), isEmpty);
    expect(service.responses, isEmpty);

    auth.grant(true);
    await container.pump();
    expect(service.responses, hasLength(1));
    auth.grant(false);
    expect(await container.read(provider.future), isEmpty);
    service.responses.first.complete(const [
      ListenLink(instanceId: 'abs', name: 'Old result', state: 'unverified')
    ]);
    await container.pump();
    expect(container.read(provider).requireValue, isEmpty);

    auth.grant(true);
    await container.pump();
    service.responses.last.complete(const []);
    await container.read(provider.future);
    container.read(mediaAccessRevisionProvider.notifier).state++;
    await container.pump();
    expect(service.responses, hasLength(3));
    service.responses.last.complete(const []);
    expect(await container.read(provider.future), isEmpty);
  });
}
