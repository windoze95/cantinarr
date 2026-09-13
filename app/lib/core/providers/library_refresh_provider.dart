import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Monotonic tick bumped after a successful request, repair or TV correction,
/// so corrected catalog statuses and the app shell
/// re-pulls its search-chip library snapshot immediately — the just-requested
/// title should read "Requested" on the very next search instead of waiting
/// for a websocket ping or a throttled focus refresh.
final libraryRefreshTickProvider = StateProvider<int>((_) => 0);
