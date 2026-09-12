import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';
import '../data/listening_apps.dart';
import '../data/media_access_service.dart';

final listeningAppPreferencesProvider =
    FutureProvider.autoDispose<ListeningApps>((ref) {
  // An account or server change must never display another person's choices.
  ref.watch(authProvider);
  return ref.watch(mediaAccessServiceProvider).getListeningAppPreferences();
});
