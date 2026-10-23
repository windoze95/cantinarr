import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('missing server capability never enables explicit import submission',
      () {
    expect(ServerConfig.fromJson({}).mediaUserImportExplicitLinking, isFalse);
    expect(
        const BackendConnection(
                serverUrl: 's', accessToken: 'a', refreshToken: 'r')
            .mediaUserImportExplicitLinking,
        isFalse);
  });
  test(
      'explicit import capability is read and preserved across connection copies',
      () {
    final config =
        ServerConfig.fromJson({'media_user_import_explicit_linking': true});
    final connection = BackendConnection(
        serverUrl: 's',
        accessToken: 'a',
        refreshToken: 'r',
        mediaUserImportExplicitLinking: config.mediaUserImportExplicitLinking);
    expect(
        connection
            .copyWith(serverName: 'Updated')
            .mediaUserImportExplicitLinking,
        isTrue);
    expect(
        connection
            .copyWith(mediaUserImportExplicitLinking: false)
            .mediaUserImportExplicitLinking,
        isFalse);
  });
}
