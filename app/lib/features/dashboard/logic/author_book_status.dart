import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../../request/data/book_ownership.dart';
import 'recent_book_ownership_status.dart';

/// The author page's per-title pill and format line.
///
/// A null [AuthorBookStatus] means ownership could not be determined for a
/// title — render no pill at all. It never means "nothing requested": the two
/// are different answers, and the whole point of the fourth state below is that
/// they stop rendering the same way.
class AuthorBookStatus {
  final String label;
  final Color color;

  /// The per-format line ("eBook + Audiobook requested"), or null when no
  /// format has anything to say about itself yet.
  final String? subtitle;

  const AuthorBookStatus({
    required this.label,
    required this.color,
    this.subtitle,
  });
}

/// The author page's verdict for one title.
///
/// It deliberately does not reuse `buildRecentBookStatus`: that row only ever
/// shows titles that just landed, so every card there is owned and an un-owned
/// one means the digest is stale. This page lists an author's whole tracked
/// bibliography, where "nobody has requested this yet" is the normal, expected
/// state and the one a requester is here to act on — so it gets a label of its
/// own instead of collapsing into the same blank as an unreadable state. The
/// three owned verdicts match that row's rule exactly, and both share
/// [recentBookOwnershipSubtitle] for the format line.
AuthorBookStatus? buildAuthorBookStatus(OwnedTitle title) {
  // An older Chaptarr server could not resolve format truth for this title.
  // Blindness, not absence: no pill, because any label would be a guess.
  if (!title.statusKnown) return null;

  final e = title.ownership.ebook;
  final a = title.ownership.audiobook;
  if (!e.owned && !a.owned) {
    return const AuthorBookStatus(
      label: 'Not requested',
      color: AppTheme.textSecondary,
    );
  }

  final subtitle = recentBookOwnershipSubtitle(title.ownership);
  if (e.downloaded && a.downloaded) {
    return AuthorBookStatus(
      label: 'Available',
      color: AppTheme.available,
      subtitle: subtitle,
    );
  }
  // A downloaded format is not something the requester is waiting for, so it
  // never counts toward "still coming".
  final awaiting = (e.monitored && !e.downloaded) || (a.monitored && !a.downloaded);
  if (awaiting) {
    return AuthorBookStatus(
      label: 'Requested',
      color: AppTheme.requested,
      subtitle: subtitle,
    );
  }
  // Something is on disk and nothing is pending: part of the title is simply
  // missing, with no one waiting on it.
  return AuthorBookStatus(
    label: 'Partial',
    color: AppTheme.requested,
    subtitle: subtitle,
  );
}
