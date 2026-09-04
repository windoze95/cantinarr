import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../data/album_ownership.dart';
import '../data/request_service.dart';
import 'request_button.dart';

/// The requester's request surface for one album: a single button carrying
/// the album's live state. Music has no format axis, so unlike
/// [BookFormatPanel] there are no per-format rows — one album, one action.
///
/// The same guards apply: a confirmed submission is pinned so a lagging
/// status re-read can never re-offer Request over an accepted one, ownership
/// from the owned-albums digest is layered over server truth, and an unknown
/// status fails closed rather than inviting a duplicate request.
class AlbumRequestPanel extends StatefulWidget {
  final String foreignId;
  final String title;
  final String? instanceId;

  /// The search text that surfaced this album, when it came from a search.
  /// A notification or deep-link arrival simply has none.
  final String? searchTerm;
  final RequestService service;

  /// The owned-albums digest row for this album, when it has one.
  final OwnedAlbum? ownership;
  final int refreshTick;

  final FutureOr<void> Function()? onRequestCompleted;

  /// Reports the library's own foreignAlbumId when the server resolved this
  /// album through a record filed under a different id (MusicBrainz merges).
  /// The owner should re-address the album by it (and rebuild this panel).
  final ValueChanged<String>? onCanonicalForeignId;

  const AlbumRequestPanel({
    super.key,
    required this.foreignId,
    required this.title,
    this.instanceId,
    this.searchTerm,
    required this.service,
    this.ownership,
    this.refreshTick = 0,
    this.onRequestCompleted,
    this.onCanonicalForeignId,
  });

  @override
  State<AlbumRequestPanel> createState() => _AlbumRequestPanelState();
}

class _AlbumRequestPanelState extends State<AlbumRequestPanel> {
  MusicRequestStatusDetail _serverDetail = const MusicRequestStatusDetail();
  bool _loading = true;
  bool _inFlight = false;
  int _checkGeneration = 0;
  Timer? _pendingRecheckTimer;
  String? _error;

  /// The status this panel submitted and the server confirmed, kept for as
  /// long as this album is on screen. A request Cantinarr accepted seconds
  /// ago can still read back as "not requested" until the arr record
  /// materialises; re-offering Request there invites a duplicate and reads
  /// as if the tap failed. A confirmed submission therefore outranks a later
  /// *unavailable* — but nothing else, so a denial and every live state
  /// (downloading, available) still win.
  RequestStatus? _submitted;

  /// Server truth with two layers on top: the owned-albums digest, and this
  /// panel's own confirmed submission.
  RequestStatus get _status {
    var status = _serverDetail.status;
    var known = _serverDetail.isKnown;
    final owned = widget.ownership;
    if (owned != null && status == RequestStatus.unavailable) {
      // The digest says the library tracks this album; the request read just
      // hasn't caught up. Downloaded outranks monitored.
      if (owned.downloaded) {
        status = RequestStatus.available;
        known = true;
      } else if (owned.monitored) {
        status = RequestStatus.requested;
        known = true;
      }
    }
    if (!known) {
      // Fail closed: an unknown state must not render as a fresh Request
      // button. Requested is the safest non-actionable word.
      return _submitted ?? RequestStatus.requested;
    }
    if (_submitted != null && status == RequestStatus.unavailable) {
      return _submitted!;
    }
    return status;
  }

  bool get _statusKnown => _serverDetail.isKnown || widget.ownership != null;

  @override
  void initState() {
    super.initState();
    _check();
  }

  @override
  void didUpdateWidget(covariant AlbumRequestPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    // If this panel got reused for a different album, re-fetch its state.
    if (oldWidget.foreignId != widget.foreignId ||
        oldWidget.instanceId != widget.instanceId) {
      _loading = true;
      _serverDetail = const MusicRequestStatusDetail();
      // Another album (or library) knows nothing about what was requested
      // here.
      _submitted = null;
      _error = null;
      _check();
    } else if (oldWidget.refreshTick != widget.refreshTick && !_inFlight) {
      _check();
    }
  }

  @override
  void dispose() {
    _pendingRecheckTimer?.cancel();
    super.dispose();
  }

  Future<void> _check() async {
    final foreignId = widget.foreignId;
    final generation = ++_checkGeneration;
    final detail = await widget.service.checkMusicStatusDetail(
      foreignId,
      instanceId: widget.instanceId,
    );
    if (!mounted ||
        generation != _checkGeneration ||
        foreignId != widget.foreignId) {
      return;
    }
    setState(() {
      _serverDetail = detail;
      _loading = false;
    });
    _syncPendingRecheck();
    final canonical = detail.canonicalForeignId ?? '';
    if (canonical.isNotEmpty && canonical != widget.foreignId) {
      widget.onCanonicalForeignId?.call(canonical);
    }
  }

  void _syncPendingRecheck() {
    // An approval ends without the requester doing anything; poll while the
    // request sits pending so the button catches the decision.
    if (_status != RequestStatus.pending) {
      _pendingRecheckTimer?.cancel();
      _pendingRecheckTimer = null;
      return;
    }
    _pendingRecheckTimer ??= Timer.periodic(
      const Duration(seconds: 30),
      (_) {
        if (mounted && !_inFlight) _check();
      },
    );
  }

  Future<void> _request() async {
    if (_inFlight) return;
    setState(() {
      _inFlight = true;
      _error = null;
    });
    try {
      MusicRequestSubmission? submission;
      String? failureMessage;
      var definitiveFailure = false;
      try {
        submission = await widget.service.requestAlbum(
          foreignId: widget.foreignId,
          title: widget.title,
          instanceId: widget.instanceId,
          searchTerm: widget.searchTerm,
        );
      } on RequestSubmissionException catch (e) {
        failureMessage = e.message;
        definitiveFailure = e.definitive;
      }
      if (!mounted) return;
      if (submission == null || submission.status == null) {
        await _check();
        if (!mounted) return;
        // The refresh just told us whether the album landed despite the
        // failed call. When it did, the button already shows it; a failure
        // note beside a Requested button would contradict the screen.
        final landed = _status;
        final covered = landed == RequestStatus.requested ||
            landed == RequestStatus.downloading ||
            landed == RequestStatus.available ||
            landed == RequestStatus.pending ||
            landed == RequestStatus.partial;
        if (!covered) {
          setState(() {
            _error = definitiveFailure && failureMessage != null
                ? failureMessage
                : 'The request outcome couldn’t be confirmed. The album '
                    'status was refreshed.';
          });
        }
        return;
      }
      setState(() => _submitted = submission!.status);
      if (submission.message.isNotEmpty) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(submission.message)));
      }
      await widget.onRequestCompleted?.call();
      if (mounted) await _check();
    } finally {
      if (mounted) setState(() => _inFlight = false);
      _syncPendingRecheck();
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const SizedBox(
        height: 54,
        child: Center(
          child: SizedBox(
            width: 22,
            height: 22,
            child: CircularProgressIndicator(
                strokeWidth: 2.5, color: AppTheme.accent),
          ),
        ),
      );
    }
    if (!_statusKnown && _submitted == null) {
      // The status read failed and the digest knows nothing either: say so
      // instead of guessing. No button — an outage must not mint requests.
      return Container(
        width: double.infinity,
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
          border: Border.all(color: AppTheme.border),
        ),
        child: const Text(
          'The album status could not be read. Pull to refresh and try again.',
          textAlign: TextAlign.center,
          style: TextStyle(color: AppTheme.textSecondary),
        ),
      );
    }
    return RequestButton(
      status: _status,
      isRequesting: _inFlight,
      onRequest: _request,
      error: _error,
    );
  }
}
