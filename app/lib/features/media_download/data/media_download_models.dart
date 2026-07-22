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

  const MediaDownloadChoice({
    required this.fileId,
    required this.label,
    this.subtitle,
  });
}

class MediaDownloadException implements Exception {
  final String message;

  const MediaDownloadException(this.message);

  @override
  String toString() => message;
}
