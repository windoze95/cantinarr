import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';
import '../data/media_access_service.dart';
import '../data/video_apps.dart';

final videoAppRevisionProvider = StateProvider<int>((ref) => 0);

final videoAppPreferencesProvider =
    FutureProvider.autoDispose<Map<String, VideoApps>>((ref) {
  ref.watch(authProvider);
  return ref.watch(mediaAccessServiceProvider).getVideoAppPreferences();
});
