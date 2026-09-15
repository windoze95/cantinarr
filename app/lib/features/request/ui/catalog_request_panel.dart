import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../data/request_quota.dart';
import '../data/request_service.dart';
import '../../discover/ui/book_browse_screen.dart';
import '../../discover/logic/discovery_access.dart';

typedef DeliveryKey = ({
  String mediaType,
  String foreignId,
  String instanceId,
  String provider,
  String sourceId,
  int? requestId
});

final savedDeliveryProvider = FutureProvider.autoDispose
    .family<Map<String, dynamic>, DeliveryKey>((ref, key) async {
  ref.watch(catalogDiscoveryScopeProvider);
  ref.watch(libraryRefreshTickProvider);
  final response = await ref
      .read(backendClientProvider)
      .get('/api/requests/delivery-status', queryParameters: {
    if (key.requestId != null) 'request_id': key.requestId,
    'include_live': false,
    'media_type': key.mediaType,
    'instance_id': key.instanceId,
    if (key.provider.isEmpty) 'foreign_id': key.foreignId,
    if (key.provider.isNotEmpty) 'catalog_provider': key.provider,
    if (key.provider.isNotEmpty) 'catalog_id': key.sourceId,
  });
  final data = response.data as Map<String, dynamic>;
  if (((data['delivery'] as List?) ?? [])
      .any((d) => !{'complete', 'cancelled'}.contains(d['state']))) {
    final timer = Timer(const Duration(seconds: 10), ref.invalidateSelf);
    ref.onDispose(timer.cancel);
    ref.onCancel(timer.cancel);
  }
  return data;
});

final _deliveryBookTruth = FutureProvider.autoDispose
    .family<BookRequestStatusDetail, ({String foreignId, String instanceId})>(
        (ref, key) {
  ref.watch(catalogDiscoveryScopeProvider);
  ref.watch(libraryRefreshTickProvider);
  return RequestService(backendDio: ref.read(backendClientProvider))
      .checkBookStatusDetail(key.foreignId, instanceId: key.instanceId);
});

final _deliveryMusicTruth = FutureProvider.autoDispose
    .family<MusicRequestStatusDetail, ({String foreignId, String instanceId})>(
        (ref, key) {
  ref.watch(catalogDiscoveryScopeProvider);
  ref.watch(libraryRefreshTickProvider);
  return RequestService(backendDio: ref.read(backendClientProvider))
      .checkMusicStatusDetail(key.foreignId, instanceId: key.instanceId);
});

/// Public catalog requests can be saved before the service catalog recovers.
/// Native detail pages use progressOnly to retain their established controls.
class CatalogRequestPanel extends ConsumerStatefulWidget {
  final String mediaType;
  final String foreignId;
  final String title;
  final String instanceId;
  final String provider;
  final String sourceId;
  final String? nativeId;
  final int? requestId;
  final bool progressOnly;
  final ValueChanged<String>? onCanonicalForeignId;
  const CatalogRequestPanel(
      {super.key,
      required this.mediaType,
      required this.foreignId,
      required this.title,
      required this.instanceId,
      this.provider = '',
      this.sourceId = '',
      this.nativeId,
      this.requestId,
      this.progressOnly = false,
      this.onCanonicalForeignId});
  @override
  ConsumerState<CatalogRequestPanel> createState() =>
      _CatalogRequestPanelState();
}

class _CatalogRequestPanelState extends ConsumerState<CatalogRequestPanel> {
  bool _busy = false;
  String? _error;
  String? _reportedCanonical;
  String? _knownNativeId;
  Map<String, dynamic>? _receipt;
  String? _scope;
  DeliveryKey get _key => (
        mediaType: widget.mediaType,
        foreignId: widget.foreignId,
        instanceId: widget.instanceId,
        provider: widget.provider,
        sourceId: widget.sourceId,
        requestId: widget.requestId
      );

  @override
  void didUpdateWidget(covariant CatalogRequestPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.foreignId != widget.foreignId ||
        oldWidget.instanceId != widget.instanceId ||
        oldWidget.requestId != widget.requestId) {
      _knownNativeId = null;
      _receipt = null;
      _reportedCanonical = null;
      _busy = false;
      _error = null;
    }
  }

  Future<void> _perform(
      {String? format,
      int? requestId,
      String? action,
      String? nativeId}) async {
    if (_busy) return;
    final key = _key;
    final scope = ref.read(catalogDiscoveryScopeProvider);
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final dio = ref.read(backendClientProvider);
      final Response response;
      if (requestId == null) {
        response = await dio.post('/api/requests', data: {
          'media_type': widget.mediaType,
          'title': widget.title,
          'instance_id': widget.instanceId,
          if (widget.provider.isEmpty) 'foreign_id': widget.foreignId,
          if (widget.provider.isNotEmpty)
            'catalog_ref': {'provider': widget.provider, 'id': widget.sourceId},
          if (format != null) 'book_format': format,
        });
      } else {
        response = await dio.post('/api/requests/$requestId/delivery', data: {
          'action': action,
          if (nativeId != null) 'foreign_id': nativeId
        });
      }
      if (!mounted ||
          _key != key ||
          ref.read(catalogDiscoveryScopeProvider) != scope) {
        return;
      }
      setState(
          () => _receipt = Map<String, dynamic>.from(response.data as Map));
      ref.invalidate(savedDeliveryProvider(key));
    } on DioException catch (e) {
      if (!mounted ||
          _key != key ||
          ref.read(catalogDiscoveryScopeProvider) != scope) {
        return;
      }
      final quota = requestQuotaError(e);
      if (quota != null) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(quota)),
        );
        return;
      }
      final body = e.response?.data;
      setState(() => _error = body is Map && body['error'] is String
          ? body['error'] as String
          : 'Could not update this request. Please retry.');
    } catch (_) {
      if (mounted &&
          _key == key &&
          ref.read(catalogDiscoveryScopeProvider) == scope) {
        setState(() => _error = 'Could not update this request. Please retry.');
      }
    } finally {
      if (mounted &&
          _key == key &&
          ref.read(catalogDiscoveryScopeProvider) == scope) {
        setState(() => _busy = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final scope = ref.watch(catalogDiscoveryScopeProvider);
    if (_scope != scope) {
      _scope = scope;
      _knownNativeId = null;
      _receipt = null;
      _reportedCanonical = null;
      _busy = false;
      _error = null;
    }
    final key = _key;
    final saved = ref.watch(savedDeliveryProvider(key));
    final data = _receipt ?? saved.valueOrNull;
    final deliveries = ((data?['delivery'] as List?) ?? []).cast<Map>();
    final active = deliveries
        .where((d) => !{'complete', 'cancelled'}.contains(d['state']))
        .toList();
    if (widget.progressOnly && active.isEmpty) return const SizedBox.shrink();
    final canonical = _knownNativeId ??
        data?['canonical_foreign_id'] as String? ??
        widget.nativeId;
    if (canonical != null &&
        canonical.isNotEmpty &&
        _reportedCanonical != canonical) {
      _reportedCanonical = canonical;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _key == key && _scope == scope) {
          widget.onCanonicalForeignId?.call(canonical);
        }
      });
    }
    final nativeId = canonical;
    final bookTruth = widget.mediaType == 'book' && nativeId != null
        ? ref
            .watch(_deliveryBookTruth(
                (foreignId: nativeId, instanceId: widget.instanceId)))
            .valueOrNull
        : null;
    final musicTruth = widget.mediaType == 'music'
        ? ref
            .watch(_deliveryMusicTruth((
              foreignId: nativeId ?? widget.foreignId,
              instanceId: widget.instanceId
            )))
            .valueOrNull
        : null;
    final truthCanonical =
        bookTruth?.canonicalForeignId ?? musicTruth?.canonicalForeignId;
    if (truthCanonical != null &&
        truthCanonical.isNotEmpty &&
        truthCanonical != nativeId &&
        _knownNativeId != truthCanonical) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _key == key && _scope == scope) {
          setState(() => _knownNativeId = truthCanonical);
        }
      });
    }
    ref.listen(savedDeliveryProvider(key), (previous, next) {
      if (next.hasValue && !next.isLoading) _receipt = next.valueOrNull;
      if (nativeId != null &&
          previous?.hasValue == true &&
          next.hasValue &&
          previous?.valueOrNull != next.valueOrNull) {
        if (widget.mediaType == 'book') {
          ref.invalidate(_deliveryBookTruth(
              (foreignId: nativeId, instanceId: widget.instanceId)));
        } else {
          ref.invalidate(_deliveryMusicTruth(
              (foreignId: nativeId, instanceId: widget.instanceId)));
        }
      }
    });
    final pendingFormats =
        active.map((d) => d['format'] as String? ?? '').toSet();
    final requestable = <String>[];
    if (widget.mediaType == 'book') {
      for (final format in [
        BookRequestFormat.ebook,
        BookRequestFormat.audiobook
      ]) {
        final status = bookTruth?.formats[format];
        final covered = bookTruth?.isKnown == true &&
            status != null &&
            !{RequestStatus.unavailable, RequestStatus.denied}.contains(status);
        if (!pendingFormats.contains(format.value) && !covered) {
          requestable.add(format.value);
        }
      }
    } else if (active.isEmpty &&
        !(musicTruth?.isKnown == true && musicTruth?.isRequestable == false)) {
      requestable.add('');
    }
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if (!widget.progressOnly &&
          widget.mediaType == 'book' &&
          bookTruth?.isKnown == true)
        for (final format in [
          BookRequestFormat.ebook,
          BookRequestFormat.audiobook
        ])
          if (!requestable.contains(format.value) &&
              !pendingFormats.contains(format.value))
            Padding(
                padding: const EdgeInsets.symmetric(vertical: 8),
                child: Row(children: [
                  Text(format.label),
                  const SizedBox(width: 16),
                  Text(bookTruth!.formats[format]?.label ?? 'Requested')
                ])),
      if (_error != null)
        Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(_error!,
                style: TextStyle(color: Theme.of(context).colorScheme.error))),
      for (final delivery in active)
        ListTile(
          contentPadding: EdgeInsets.zero,
          title: Text(
              '${delivery['format'] == 'ebook' ? 'eBook · ' : delivery['format'] == 'audiobook' ? 'Audiobook · ' : ''}${delivery['message'] ?? 'Request saved'}'),
          subtitle: delivery['next_attempt_at'] is String
              ? Text(
                  'Next retry: ${DateTime.tryParse(delivery['next_attempt_at'] as String)?.toLocal()}')
              : null,
        ),
      if (active.isNotEmpty)
        Wrap(spacing: 12, children: [
          for (final id
              in active.map((d) => d['request_id'] as int).toSet()) ...[
            if (active.any((d) =>
                d['request_id'] == id &&
                d['can_manage'] != false &&
                d['code'] != 'catalog_retired' &&
                {'attention', 'retry'}.contains(d['state'])))
              TextButton(
                  onPressed: _busy
                      ? null
                      : () => _perform(requestId: id, action: 'retry'),
                  child: const Text('Try again')),
            if (active
                .any((d) => d['request_id'] == id && d['can_cancel'] != false))
              TextButton(
                  onPressed: _busy
                      ? null
                      : () => _perform(requestId: id, action: 'cancel'),
                  child: const Text('Cancel request')),
          ],
        ]),
      if (active.any((d) => d['code'] == 'catalog_retired'))
        NativeBookSearchButton(
            title: widget.title, instanceId: widget.instanceId),
      if (!widget.progressOnly && requestable.isNotEmpty)
        Wrap(spacing: 12, runSpacing: 8, children: [
          for (final format in requestable)
            FilledButton(
                onPressed: _busy || saved.isLoading
                    ? null
                    : () => _perform(format: format.isEmpty ? null : format),
                child: Text(format == 'ebook'
                    ? 'Request eBook'
                    : format == 'audiobook'
                        ? 'Request audiobook'
                        : 'Request album')),
          if (requestable.length == 2)
            OutlinedButton(
                onPressed: _busy || saved.isLoading
                    ? null
                    : () => _perform(format: 'both'),
                child: const Text('Request both')),
        ]),
      if (!widget.progressOnly && requestable.isEmpty && active.isEmpty)
        const Text('This title is already covered by your library.'),
      if (_busy)
        const Padding(
            padding: EdgeInsets.only(top: 12),
            child: LinearProgressIndicator()),
    ]);
  }
}
