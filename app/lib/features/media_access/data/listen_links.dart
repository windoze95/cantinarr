import 'listening_apps.dart';

class ListenItem {
  final String id, title, libraryName, url;
  final List<String> narrators;

  const ListenItem(
      {required this.id,
      required this.title,
      required this.libraryName,
      required this.url,
      this.narrators = const []});

  factory ListenItem.fromJson(Map<String, dynamic> json) => ListenItem(
        id: json['id'] as String? ?? '',
        title: json['title'] as String? ?? '',
        libraryName: json['library_name'] as String? ?? '',
        url: json['url'] as String? ?? '',
        narrators:
            (json['narrators'] as List? ?? []).whereType<String>().toList(),
      );
}

class ListenLink {
  final String instanceId, name, state, fallbackUrl;
  final List<ListenItem> items;
  final ListeningApps listeningApps;

  const ListenLink(
      {required this.instanceId,
      required this.name,
      required this.state,
      this.fallbackUrl = '',
      this.listeningApps = const ListeningApps(),
      this.items = const []});

  factory ListenLink.fromJson(Map<String, dynamic> json) => ListenLink(
        instanceId: json['instance_id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        state: json['state'] as String? ?? 'unverified',
        fallbackUrl: json['fallback_url'] as String? ?? '',
        listeningApps: ListeningApps.fromJson(json['listening_apps']),
        items: (json['items'] as List? ?? [])
            .whereType<Map>()
            .map((item) => ListenItem.fromJson(Map<String, dynamic>.from(item)))
            .toList(),
      );
}
