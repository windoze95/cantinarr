import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';

/// Whether covers mark titles whose library copy measures 4K. An admin turns
/// this on for everyone under Settings > Discover; the app follows the
/// server's config and asks for the per-show check only while it is on.
final cover4KBadgesProvider = Provider<bool>((ref) => ref.watch(authProvider
    .select((auth) => auth.valueOrNull?.connection?.cover4KBadges ?? false)));
