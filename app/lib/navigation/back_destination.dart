import 'package:flutter/widgets.dart';
import 'package:go_router/go_router.dart';

String _discoverParent(String type) => switch (type) {
      'tv' || 'tv-library' => '/dashboard/tv',
      'book' || 'books' || 'author' || 'series' => '/dashboard/books',
      'album' || 'artist' || 'music' => '/dashboard/music',
      _ => '/dashboard/movies',
    };

void backFromMediaDetail(BuildContext context, String type) {
  if (context.canPop()) {
    context.pop();
  } else {
    context.go(_discoverParent(type));
  }
}

/// A direct secondary route has no previous page. Use the same logical
/// parent as its Back button, letting the router apply current access rules.
String secondaryBackDestination(GoRouter router) {
  final segments = router.state.uri.pathSegments;
  if (segments case ['detail' || 'browse', final type, ...]) {
    return _discoverParent(type);
  }
  if (segments.firstOrNull == 'agent-runs') return '/agent-actions';
  // Settings editors and issue threads have addressable parents, but not
  // every path prefix is a page (e.g. /settings/instance).
  for (var length = segments.length - 1; length > 0; length--) {
    final parent = Uri(pathSegments: ['', ...segments.take(length)]);
    if (!router.configuration.findMatch(parent).isError) {
      return parent.toString();
    }
  }
  return '/dashboard/movies';
}
