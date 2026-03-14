/// Tracks pagination state for any paginated API.
class PagedLoader {
  int _page = 1;
  int _totalPages = 1;
  bool _isLoading = false;

  int get page => _page;
  int get totalPages => _totalPages;
  bool get isLoading => _isLoading;
  bool get hasMore => _page <= _totalPages;

  /// Returns true if loading can begin (not already loading and has more pages).
  bool beginLoading() {
    if (_isLoading || !hasMore) return false;
    _isLoading = true;
    return true;
  }

  /// Updates counters after a successful page load.
  void endLoading(int responseTotalPages) {
    _totalPages = responseTotalPages;
    _page++;
    _isLoading = false;
  }

  /// Call when a load fails to allow retrying the same page.
  void cancelLoading() {
    _isLoading = false;
  }

  /// Reset to start fresh.
  void reset() {
    _page = 1;
    _totalPages = 1;
    _isLoading = false;
  }
}
