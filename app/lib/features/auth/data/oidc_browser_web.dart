import 'package:web/web.dart' as web;

String? readTabValue(String key) => web.window.sessionStorage.getItem(key);
void writeTabValue(String key, String? value) {
  if (value == null) {
    web.window.sessionStorage.removeItem(key);
  } else {
    web.window.sessionStorage.setItem(key, value);
  }
}

void replaceBrowserLocation(String location) =>
    web.window.location.assign(location);
void clearOIDCReturnAddress() =>
    web.window.history.replaceState(null, '', '/#/oidc/return');
