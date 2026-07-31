class MediaDownloadTicket {
  final Uri url;
  final String filename;
  final int sizeBytes;
  final DateTime expiresAt;

  const MediaDownloadTicket({
    required this.url,
    required this.filename,
    required this.sizeBytes,
    required this.expiresAt,
  });
}

class MediaDownloadChoice {
  final int fileId;
  final String label;
  final String? subtitle;

  /// The file's path exactly as the arr reported it. When present, download
  /// affordances are offered only while the server does not rule the path
  /// outside this instance's media path mappings.
  final String? reportedPath;

  const MediaDownloadChoice({
    required this.fileId,
    required this.label,
    this.subtitle,
    this.reportedPath,
  });
}

class MediaDownloadException implements Exception {
  final String message;

  const MediaDownloadException(this.message);

  @override
  String toString() => message;
}
