import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/media_access_service.dart';
import '../data/listen_links.dart';

typedef ListenRequest = ({
  String instanceId,
  String foreignId,
  int refreshTick
});

final mediaAccessRevisionProvider = StateProvider<int>((ref) => 0);

final listenLinksProvider = FutureProvider.autoDispose
    .family<List<ListenLink>, ListenRequest>((ref, request) async {
  final auth = ref.watch(authProvider).asData?.value;
  if (!(auth?.connection?.mediaServerInstances
          .any((server) => server.serviceType == 'audiobookshelf') ??
      false)) {
    return const [];
  }
  ref.watch(mediaAccessRevisionProvider);
  ref.watch(libraryRefreshTickProvider);
  ref.watch(libraryChangedEventsProvider);
  return ref.watch(mediaAccessServiceProvider).listenLinks(
      instanceId: request.instanceId, foreignBookId: request.foreignId);
});
