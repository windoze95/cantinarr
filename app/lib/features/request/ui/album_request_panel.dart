import 'dart:async';
import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../data/album_ownership.dart';
import '../data/request_service.dart';

/// One stable action carries both saved intent and independently read live
/// availability. A failed or lagging refresh never erases an accepted receipt.
class AlbumRequestPanel extends StatefulWidget {
  final String foreignId;
  final String title;
  final String? instanceId;
  final String? searchTerm;
  final RequestService service;
  final OwnedAlbum? ownership;
  final int refreshTick;
  final FutureOr<void> Function()? onRequestCompleted;
  final ValueChanged<String>? onCanonicalForeignId;
  const AlbumRequestPanel(
      {super.key,
      required this.foreignId,
      required this.title,
      this.instanceId,
      this.searchTerm,
      required this.service,
      this.ownership,
      this.refreshTick = 0,
      this.onRequestCompleted,
      this.onCanonicalForeignId});
  @override
  State<AlbumRequestPanel> createState() => _AlbumRequestPanelState();
}

class _AlbumRequestPanelState extends State<AlbumRequestPanel> {
  Map<String, dynamic>? _receipt;
  MusicRequestStatusDetail? _live;
  RequestStatus? _submitted;
  String? _canonical;
  String? _error;
  String? _refreshError;
  bool _checking = true;
  bool _busy = false;
  int _generation = 0;
  int _savedRead = 0;
  int _liveRead = 0;
  Timer? _timer;
  List<Map> get _deliveries =>
      (_receipt?['delivery'] as List? ?? const []).cast<Map>();
  List<Map> get _active => _deliveries
      .where((d) => !{'complete', 'cancelled'}.contains(d['state']))
      .toList();
  int? get _requestId => (_receipt?['request_id'] as num?)?.toInt();
  @override
  void initState() {
    super.initState();
    _refresh();
    _timer = Timer.periodic(const Duration(seconds: 10), (_) {
      if (!_busy) _refresh();
    });
  }

  @override
  void didUpdateWidget(covariant AlbumRequestPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.foreignId != widget.foreignId ||
        oldWidget.instanceId != widget.instanceId ||
        oldWidget.service != widget.service) {
      _generation++;
      _receipt = null;
      _live = null;
      _canonical = null;
      _submitted = null;
      _busy = false;
      _error = null;
      _refreshError = null;
      _checking = true;
      _refresh();
    } else if (oldWidget.refreshTick != widget.refreshTick && !_busy) {
      _refresh();
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  void _refresh() {
    unawaited(_saved());
    unawaited(_availability());
  }

  void _follow(String? id) {
    if (id == null || id.isEmpty || id == _canonical) return;
    _canonical = id;
    widget.onCanonicalForeignId?.call(id);
  }

  Future<void> _saved() async {
    final generation = _generation;
    final read = ++_savedRead;
    try {
      final data = await widget.service
          .musicDeliveryStatus(widget.foreignId,
              instanceId: widget.instanceId, requestId: _requestId)
          .timeout(const Duration(seconds: 10));
      if (!mounted || generation != _generation || read != _savedRead) return;
      final previous = _requestId ?? 0;
      final incoming = (data['request_id'] as num?)?.toInt() ?? 0;
      setState(() {
        if (incoming >= previous) {
          _receipt = data;
          if (incoming > 0 && _submitted != null) {
            for (final status in RequestStatus.values) {
              if (status.name == data['status']) _submitted = status;
            }
          }
          if (_deliveries.any((d) => d['state'] == 'cancelled')) {
            _submitted = null;
          }
        }
        _checking = false;
        _refreshError = null;
      });
      _follow(data['canonical_foreign_id'] as String?);
    } catch (_) {
      if (mounted && generation == _generation && read == _savedRead) {
        setState(() {
          _checking = false;
          _refreshError = 'Saved request updates could not be checked.';
        });
      }
    }
  }

  Future<void> _availability() async {
    final generation = _generation;
    final read = ++_liveRead;
    final detail = await widget.service
        .checkMusicStatusDetail(_canonical ?? widget.foreignId,
            instanceId: widget.instanceId)
        .timeout(const Duration(seconds: 10),
            onTimeout: () => const MusicRequestStatusDetail(isKnown: false));
    if (!mounted || generation != _generation || read != _liveRead) return;
    setState(() => _live = detail);
    _follow(detail.canonicalForeignId);
  }

  String get _label {
    final owned = widget.ownership;
    if (_live?.isKnown == true && _live?.status == RequestStatus.available) {
      return 'Available';
    }
    if (owned?.status == 'available' ||
        (owned?.status == null && owned?.downloaded == true)) {
      return 'Available';
    }
    if (_live?.isKnown == true && _live?.status == RequestStatus.downloading) {
      return 'Downloading';
    }
    if (_active.any((d) => d['state'] == 'approval') ||
        _submitted == RequestStatus.pending ||
        (_live?.isKnown == true && _live?.status == RequestStatus.pending)) {
      return 'Waiting for approval';
    }
    if (_active.any((d) => {'attention', 'needs_match'}.contains(d['state']))) {
      return 'Needs attention';
    }
    if (_active.isNotEmpty) return 'Requested';
    if (_live?.isKnown == true && _live?.status == RequestStatus.requested ||
        owned?.monitored == true) {
      return 'Requested';
    }
    if (_submitted != null && _submitted != RequestStatus.denied) {
      return 'Requested';
    }
    if (_deliveries.any((d) => d['state'] == 'complete') &&
        _live?.isKnown != true) {
      return 'Requested';
    }
    return 'Request';
  }

  Future<void> _perform({String? action, int? requestId}) async {
    if (_busy) return;
    final generation = _generation;
    _savedRead++;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      Map<String, dynamic>? receipt;
      RequestStatus? status;
      if (action == null) {
        final submitted = await widget.service.requestAlbum(
            foreignId: _canonical ?? widget.foreignId,
            title: widget.title,
            instanceId: widget.instanceId,
            searchTerm: widget.searchTerm);
        receipt = submitted?.receipt;
        status = submitted?.status;
        if (status == null) throw const FormatException('Unconfirmed request');
      } else {
        receipt = await widget.service.musicDeliveryAction(requestId!, action);
      }
      if (!mounted || generation != _generation) return;
      setState(() {
        _savedRead++;
        if (receipt != null) _receipt = receipt;
        _submitted = action == 'cancel' ? null : status ?? _submitted;
      });
      _follow(receipt?['canonical_foreign_id'] as String?);
      unawaited(Future<void>.sync(() => widget.onRequestCompleted?.call()));
      _refresh();
    } on RequestSubmissionException catch (e) {
      if (mounted && generation == _generation) {
        if (e.quotaExceeded) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text(e.message)),
          );
        } else {
          setState(() => _error = e.message);
        }
      }
    } catch (_) {
      if (mounted && generation == _generation) {
        setState(() => _error =
            'The request outcome could not be confirmed. Try refreshing its saved status.');
      }
    } finally {
      if (mounted && generation == _generation) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final label = _label;
    final available = label == 'Available';
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      SizedBox(
          height: 54,
          child: FilledButton.icon(
            onPressed: !_busy && !_checking && label == 'Request'
                ? () => _perform()
                : null,
            icon: Icon(available
                ? Icons.check_circle
                : label == 'Needs attention'
                    ? Icons.error_outline
                    : label == 'Request'
                        ? Icons.add
                        : Icons.hourglass_top),
            label: Text(_busy
                ? 'Saving…'
                : _checking && _receipt == null
                    ? 'Checking request…'
                    : label),
          )),
      if (_active.isNotEmpty && !available)
        Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(
                _active.first['message'] as String? ?? 'Your request is saved.',
                style: Theme.of(context).textTheme.bodySmall)),
      if (_active.isNotEmpty)
        Wrap(spacing: 12, children: [
          for (final id in _active
              .map((d) => (d['request_id'] as num).toInt())
              .toSet()) ...[
            if (_active.any((d) =>
                d['request_id'] == id &&
                d['can_manage'] == true &&
                {'attention', 'retry'}.contains(d['state'])))
              TextButton(
                  onPressed: _busy
                      ? null
                      : () => _perform(action: 'retry', requestId: id),
                  child: const Text('Try again')),
            if (_active
                .any((d) => d['request_id'] == id && d['can_cancel'] == true))
              TextButton(
                  onPressed: _busy
                      ? null
                      : () => _perform(action: 'cancel', requestId: id),
                  child: const Text('Cancel request')),
          ]
        ]),
      if (_error != null)
        Text(_error!, style: const TextStyle(color: AppTheme.error)),
      if (_refreshError != null || _live?.isKnown == false)
        TextButton(
            onPressed: _refresh,
            child: Text(_refreshError ??
                'Library availability could not be checked. Retry')),
    ]);
  }
}
