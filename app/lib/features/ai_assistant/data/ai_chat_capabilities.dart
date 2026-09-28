import 'package:flutter/foundation.dart';

const aiChatWelcomeFallback =
    'Hey! I\'m your Cantinarr assistant. What would you like to do?';

/// Presentation hints from the server's current tool and library permissions.
/// Missing hints never imply that a capability is available.
class AiChatCapabilities {
  final List<String> discoverMediaTypes;
  final List<String> requestMediaTypes;
  final bool checkAvailability;
  final bool browseLibraries;
  final bool checkDownloads;
  final bool manageDownloads;
  final bool troubleshoot;
  final bool configureServices;

  const AiChatCapabilities({
    this.discoverMediaTypes = const [],
    this.requestMediaTypes = const [],
    this.checkAvailability = false,
    this.browseLibraries = false,
    this.checkDownloads = false,
    this.manageDownloads = false,
    this.troubleshoot = false,
    this.configureServices = false,
  });

  factory AiChatCapabilities.fromJson(Map<String, dynamic> json) =>
      AiChatCapabilities(
        discoverMediaTypes: _mediaTypes(json['discover_media_types']),
        requestMediaTypes: _mediaTypes(json['request_media_types']),
        checkAvailability: json['check_availability'] == true,
        browseLibraries: json['browse_libraries'] == true,
        checkDownloads: json['check_downloads'] == true,
        manageDownloads: json['manage_downloads'] == true,
        troubleshoot: json['troubleshoot'] == true,
        configureServices: json['configure_services'] == true,
      );

  String get welcomeMessage {
    final actions = <String>[];
    if (discoverMediaTypes.isNotEmpty &&
        listEquals(discoverMediaTypes, requestMediaTypes)) {
      actions.add('discover and request ${_mediaNames(discoverMediaTypes)}');
    } else {
      if (discoverMediaTypes.isNotEmpty) {
        actions.add('discover ${_mediaNames(discoverMediaTypes)}');
      }
      if (requestMediaTypes.isNotEmpty) {
        actions.add('request ${_mediaNames(requestMediaTypes)}');
      }
    }
    if (checkAvailability) actions.add('check what\'s available');
    if (browseLibraries) actions.add('browse your libraries');
    if (manageDownloads) {
      actions.add('manage downloads');
    } else if (checkDownloads) {
      actions.add('check downloads');
    }
    if (troubleshoot) actions.add('troubleshoot issues');
    if (configureServices) actions.add('configure connected services');
    if (actions.isEmpty) return aiChatWelcomeFallback;
    return 'Hey! I\'m your Cantinarr assistant. I can help you '
        '${_join(actions)}. What would you like to do?';
  }

  List<String> get suggestions => [
        if (discoverMediaTypes.contains('movie')) 'Recommend a movie',
        if (discoverMediaTypes.contains('tv')) 'Find a TV show',
        if (discoverMediaTypes.contains('book')) 'Find a book',
        if (discoverMediaTypes.contains('music')) 'Find an album',
        if (checkAvailability) 'What\'s available in my library?',
        if (troubleshoot) 'Check for download problems',
        if (configureServices) 'Help configure my services',
      ].take(4).toList();

  static const _labels = {
    'movie': 'movies',
    'tv': 'TV shows',
    'book': 'books',
    'music': 'music',
  };

  static List<String> _mediaTypes(dynamic value) => value is List
      ? _labels.keys.where(value.contains).toList()
      : const [];

  static String _mediaNames(List<String> values) =>
      _join(values.map((value) => _labels[value]!).toList());

  static String _join(List<String> values) => switch (values.length) {
        0 => '',
        1 => values.single,
        2 => '${values.first} and ${values.last}',
        _ => '${values.take(values.length - 1).join(', ')}, and ${values.last}',
      };
}
