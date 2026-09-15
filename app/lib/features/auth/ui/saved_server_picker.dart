import 'package:flutter/material.dart';
import '../logic/saved_servers_provider.dart';

/// Shared visual identity for the selected-server badge and saved shortcuts.
class ServerBadge extends StatelessWidget {
  final SavedServer server;
  final VoidCallback? onChange;

  const ServerBadge({super.key, required this.server, this.onChange});

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return OutlinedButton(
      onPressed: onChange,
      style: OutlinedButton.styleFrom(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        foregroundColor: colors.onSurface,
      ),
      child: Row(
        children: [
          const Icon(Icons.dns_outlined, size: 22),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(server.label,
                    maxLines: 2, overflow: TextOverflow.ellipsis),
                if (server.name != null)
                  Text(server.url,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.bodySmall),
                const SizedBox(height: 4),
                Text('Change server',
                    style: Theme.of(context)
                        .textTheme
                        .labelMedium
                        ?.copyWith(color: colors.primary)),
              ],
            ),
          ),
          const Icon(Icons.expand_more),
        ],
      ),
    );
  }
}

class SavedServerPicker extends StatelessWidget {
  final List<SavedServer> servers;
  final String selectedUrl;
  final ValueChanged<SavedServer> onSelect;
  final ValueChanged<SavedServer> onForget;

  const SavedServerPicker({
    super.key,
    required this.servers,
    required this.selectedUrl,
    required this.onSelect,
    required this.onForget,
  });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('Saved servers', style: Theme.of(context).textTheme.titleSmall),
        const SizedBox(height: 8),
        for (final (index, server) in servers.indexed)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Material(
              color: server.url == selectedUrl
                  ? colors.primaryContainer
                  : colors.surfaceContainerHigh,
              borderRadius: BorderRadius.circular(12),
              clipBehavior: Clip.antiAlias,
              child: ListTile(
                key: ValueKey('saved-server-${server.url}'),
                onTap: () => onSelect(server),
                leading: Icon(server.url == selectedUrl
                    ? Icons.check_circle_outline
                    : Icons.dns_outlined),
                title: Text(server.label,
                    maxLines: 2, overflow: TextOverflow.ellipsis),
                subtitle: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (server.name != null)
                      Text(server.url,
                          maxLines: 2, overflow: TextOverflow.ellipsis),
                    if (index == 0) const Text('Last used'),
                    if (server.url == selectedUrl) const Text('Selected'),
                  ],
                ),
                trailing: PopupMenuButton<String>(
                  tooltip: 'Options for ${server.label}',
                  onSelected: (_) => onForget(server),
                  itemBuilder: (_) => const [
                    PopupMenuItem(value: 'forget', child: Text('Forget')),
                  ],
                ),
              ),
            ),
          ),
        const SizedBox(height: 16),
      ],
    );
  }
}
