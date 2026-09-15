import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/widgets/app_sheet.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/tmdb_models.dart';
import '../data/apple_tv_service.dart';

class AppleTVOpenButton extends ConsumerStatefulWidget {
  const AppleTVOpenButton({super.key, required this.mediaType, required this.tmdbId});
  final MediaType mediaType;
  final int tmdbId;

  @override
  ConsumerState<AppleTVOpenButton> createState() => _AppleTVOpenButtonState();
}

class _AppleTVOpenButtonState extends ConsumerState<AppleTVOpenButton>
    with WidgetsBindingObserver {
  bool _sending = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }
  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) ref.invalidate(appleTVsProvider);
  }

  Future<void> _open(List<AppleTV> devices) async {
    if (_sending) return;
    setState(() => _sending = true);
    final auth = ref.read(authProvider).valueOrNull;
    final mediaType = widget.mediaType;
    final tmdbId = widget.tmdbId;
    final tv = devices.length == 1 ? devices.single : await showAppSheet<AppleTV>(
      context, builder: (context) => AppSheet(child: Column(
        mainAxisSize: MainAxisSize.min, children: [
          Text('Open on Apple TV', style: Theme.of(context).textTheme.titleLarge),
          for (final tv in devices) ListTile(leading: const Icon(Icons.tv),
            title: Text(tv.name), onTap: () => Navigator.pop(context, tv)),
          const SizedBox(height: 16),
        ],
      )),
    );
    if (!mounted) return;
    final selectedAuth = ref.read(authProvider).valueOrNull;
    if (tv == null || selectedAuth?.user?.id != auth?.user?.id ||
        selectedAuth?.connection?.serverUrl != auth?.connection?.serverUrl ||
        widget.mediaType != mediaType || widget.tmdbId != tmdbId) {
      setState(() => _sending = false);
      return;
    }
    final service = ref.read(appleTVServiceProvider);
    try {
      final result = await service.open(tv.id, mediaType.name, tmdbId);
      if (!mounted) return;
      final current = ref.read(authProvider).valueOrNull;
      if (current?.user?.id != auth?.user?.id ||
          current?.connection?.serverUrl != auth?.connection?.serverUrl ||
          widget.mediaType != mediaType || widget.tmdbId != tmdbId) {
        return;
      }
      await showAppSheet<void>(context, builder: (_) => _HandoffSheet(
        tv: tv, handoff: result, service: service,
      ));
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(appleTVError(error))));
      }
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authProvider).valueOrNull;
    if (auth?.connection?.appleTvRemote != true || auth?.user == null ||
        auth!.user!.child) {
      return const SizedBox.shrink();
    }
    final tvs = ref.watch(appleTVsProvider);
    return tvs.when(
      skipLoadingOnReload: false,
      data: (list) {
        if (!list.supported || list.devices.isEmpty) return const SizedBox.shrink();
        return TextButton.icon(
          onPressed: _sending ? null : () => _open(list.devices),
          icon: const Icon(Icons.tv, size: 17),
          label: Text(_sending ? 'Sending to TV…' : list.devices.length == 1
            ? 'Open on ${list.devices.single.name}' : 'Open on Apple TV'),
        );
      },
      loading: () => const SizedBox.shrink(),
      error: (_, __) => TextButton.icon(
        onPressed: () => ref.invalidate(appleTVsProvider),
        icon: const Icon(Icons.refresh, size: 17),
        label: const Text('Couldn’t load Apple TVs · Retry'),
      ),
    );
  }
}

class _HandoffSheet extends StatefulWidget {
  const _HandoffSheet({required this.tv, required this.handoff, required this.service});
  final AppleTV tv;
  final AppleTVHandoff handoff;
  final AppleTVService service;
  @override
  State<_HandoffSheet> createState() => _HandoffSheetState();
}

class _HandoffSheetState extends State<_HandoffSheet> {
  Timer? _timer;
  bool _used = false;
  bool _sending = false;
  String? _error;
  bool get _expired => !DateTime.now().isBefore(widget.handoff.expiresAt);

  @override
  void initState() {
    super.initState();
    _timer = Timer(widget.handoff.expiresAt.difference(DateTime.now()), () {
      if (mounted) setState(() {});
    });
  }
  @override
  void dispose() { _timer?.cancel(); super.dispose(); }

  Future<void> _confirm() async {
    if (_used || _expired) return;
    setState(() { _used = true; _sending = true; });
    try {
      await widget.service.confirm(widget.tv.id, widget.handoff.id);
      if (mounted) Navigator.pop(context);
    } catch (error) {
      if (mounted) setState(() => _error = appleTVError(error));
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) => AppSheet(child: Column(
    mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text('Sent to ${widget.tv.name}', style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 12),
      const Text('Check your TV. If it asks “Open in Infuse”, use Confirm Open. '
        'If the title is already open, tap Done.'),
      const SizedBox(height: 8),
      const Text('Infuse uses the libraries connected on that TV.'),
      if (_error != null) Padding(padding: const EdgeInsets.only(top: 12),
        child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error))),
      const SizedBox(height: 16),
      Wrap(spacing: 12, children: [
        FilledButton(onPressed: _used || _expired ? null : _confirm,
          child: Text(_sending ? 'Confirming…' : _expired ? 'Confirmation expired' : 'Confirm Open')),
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Done')),
      ]),
      const SizedBox(height: 24),
    ],
  ));
}
