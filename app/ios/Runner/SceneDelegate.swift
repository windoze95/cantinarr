import Flutter
import UIKit
import app_links

class SceneDelegate: FlutterSceneDelegate {
  override func scene(
    _ scene: UIScene,
    willConnectTo session: UISceneSession,
    options connectionOptions: UIScene.ConnectionOptions
  ) {
    super.scene(scene, willConnectTo: session, options: connectionOptions)

    // A cold browser return arrives in scene connection options. The pinned
    // app_links plugin receives warm AppDelegate callbacks but does not read
    // these options, so retain the initial URL until Dart can consume it.
    if let url = connectionOptions.urlContexts.first?.url,
       url.scheme == "cantinarr" {
      AppLinks.shared.handleLink(url: url)
    }
  }
}
