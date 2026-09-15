import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../../settings/data/discovery_settings_service.dart';
import '../logic/discovery_access.dart';
import 'catalog_setup_button.dart';

/// A fixed sibling of the scrolling catalog, above the module navigation.
class CatalogSetupFooter extends ConsumerStatefulWidget {
  final String mediaType;
  const CatalogSetupFooter({super.key, required this.mediaType});

  @override
  ConsumerState<CatalogSetupFooter> createState() => _CatalogSetupFooterState();
}

class _CatalogSetupFooterState extends ConsumerState<CatalogSetupFooter> {
  bool _saving = false;
  String? _error;

  Future<void> _hide() async {
    if (_saving) return;
    setState(() {
      _saving = true;
      _error = null;
    });
    final auth = ref.read(authProvider.notifier);
    // The broadcast may arrive before PUT completes. Apply visibility only
    // after the save succeeds, and report either save/refresh failure in place.
    auth.deferConfigRefresh();
    var saved = false;
    try {
      await DiscoverySettingsService(
              backendDio: ref.read(backendClientProvider))
          .setHidden(widget.mediaType, true);
      saved = true;
    } catch (_) {
      if (mounted) {
        setState(() => _error = 'Could not hide this tab. Try again.');
      }
    } finally {
      try {
        await auth.resumeConfigRefresh();
      } catch (_) {
        if (mounted && saved) {
          setState(
              () => _error = 'Saved, but could not refresh tabs. Try again.');
        }
      }
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final access = ref.watch(discoveryAccessProvider);
    final catalog =
        discoverCatalogs.firstWhere((tab) => tab.mediaType == widget.mediaType);
    if (!access.needsSetup(catalog.serviceType)) return const SizedBox.shrink();
    final canHide = access.connection?.hiddenDiscoverTabs != null;
    return Material(
      color: AppTheme.surface,
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
          child: LayoutBuilder(builder: (context, constraints) {
            final stacked = constraints.maxWidth < 420 ||
                MediaQuery.textScalerOf(context).scale(14) > 20;
            final setup = CatalogSetupButton(
                serviceType: catalog.serviceType,
                label: 'Set up ${catalog.serviceName}');
            final hide = OutlinedButton.icon(
              onPressed: canHide && !_saving ? _hide : null,
              icon: _saving
                  ? const SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(strokeWidth: 2))
                  : const Icon(Icons.visibility_off_outlined),
              label: Text(_saving ? 'Hiding…' : 'Hide this tab'),
            );
            return Column(mainAxisSize: MainAxisSize.min, children: [
              const Text(
                  'Hiding this tab affects everyone on this server. '
                  'It returns automatically when its service is connected.',
                  style:
                      TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
              if (!canHide)
                const Padding(
                    padding: EdgeInsets.only(top: 6),
                    child: Text(discoverVisibilityUpdateMessage)),
              if (_error != null)
                Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Text(_error!,
                        style: const TextStyle(color: AppTheme.error))),
              const SizedBox(height: 8),
              if (stacked)
                Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      setup,
                      const SizedBox(height: 8),
                      hide,
                    ])
              else
                Row(children: [
                  Expanded(child: setup),
                  const SizedBox(width: 12),
                  Expanded(child: hide)
                ]),
            ]);
          }),
        ),
      ),
    );
  }
}
