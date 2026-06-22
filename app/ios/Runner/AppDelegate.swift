import Flutter
import UIKit
import UserNotifications

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  /// Method channel shared with the Dart `PushService`. Created once the
  /// implicit Flutter engine is initialized.
  private var pushChannel: FlutterMethodChannel?

  /// The most recently obtained APNs device token, hex-encoded. Cached so a
  /// late Dart `getApnsToken` call can retrieve it even if the channel wasn't
  /// ready when the token first arrived from APNs.
  private var apnsToken: String?

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    // Show foreground notifications natively (no flutter_local_notifications).
    UNUserNotificationCenter.current().delegate = self
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)

    // The application registrar vends the implicit engine's binary messenger,
    // which is the correct messenger for app-level channels (the root view
    // controller may not exist yet under the UIScene lifecycle).
    let messenger = engineBridge.applicationRegistrar.messenger()
    let channel = FlutterMethodChannel(
      name: "codes.julian.cantinarr/push",
      binaryMessenger: messenger
    )
    channel.setMethodCallHandler { [weak self] call, result in
      self?.handleMethodCall(call, result: result)
    }
    pushChannel = channel

    // If a token arrived before the channel existed, deliver it now.
    if let token = apnsToken {
      channel.invokeMethod("onApnsToken", arguments: token)
    }
  }

  // MARK: - Dart -> Native

  private func handleMethodCall(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    switch call.method {
    case "requestPermission":
      requestNotificationPermission { granted in result(granted) }
    case "getApnsToken":
      // Returns the cached token (or nil if not yet registered).
      result(apnsToken)
    case "getAuthorizationStatus":
      fetchAuthorizationStatus { status in result(status) }
    case "openNotificationSettings":
      openNotificationSettings { opened in result(opened) }
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  /// Reports the current notification authorization status as a string the
  /// Dart side can interpret: `authorized`, `denied`, `notDetermined`,
  /// `provisional`, or `ephemeral`.
  private func fetchAuthorizationStatus(completion: @escaping (String) -> Void) {
    UNUserNotificationCenter.current().getNotificationSettings { settings in
      let status: String
      switch settings.authorizationStatus {
      case .authorized:
        status = "authorized"
      case .denied:
        status = "denied"
      case .notDetermined:
        status = "notDetermined"
      case .provisional:
        status = "provisional"
      case .ephemeral:
        status = "ephemeral"
      @unknown default:
        status = "notDetermined"
      }
      completion(status)
    }
  }

  /// Opens this app's page in the system Settings so the user can toggle
  /// notification permissions. `completion` reports whether the URL opened.
  private func openNotificationSettings(completion: @escaping (Bool) -> Void) {
    guard let url = URL(string: UIApplication.openSettingsURLString) else {
      completion(false)
      return
    }
    DispatchQueue.main.async {
      if UIApplication.shared.canOpenURL(url) {
        UIApplication.shared.open(url, options: [:]) { success in
          completion(success)
        }
      } else {
        completion(false)
      }
    }
  }

  /// Requests notification authorization and, if granted, registers with APNs.
  /// `completion` reports whether authorization was granted.
  private func requestNotificationPermission(completion: @escaping (Bool) -> Void) {
    UNUserNotificationCenter.current().requestAuthorization(
      options: [.alert, .sound, .badge]
    ) { granted, error in
      if let error = error {
        NSLog("Notification authorization error: \(error.localizedDescription)")
      }
      if granted {
        DispatchQueue.main.async {
          UIApplication.shared.registerForRemoteNotifications()
        }
      }
      completion(granted)
    }
  }

  // MARK: - APNs registration callbacks

  override func application(
    _ application: UIApplication,
    didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
  ) {
    // Let Flutter forward the raw token to any registered plugins first.
    super.application(application, didRegisterForRemoteNotificationsWithDeviceToken: deviceToken)

    let token = deviceToken.map { String(format: "%02x", $0) }.joined()
    apnsToken = token
    pushChannel?.invokeMethod("onApnsToken", arguments: token)
  }

  override func application(
    _ application: UIApplication,
    didFailToRegisterForRemoteNotificationsWithError error: Error
  ) {
    super.application(application, didFailToRegisterForRemoteNotificationsWithError: error)
    NSLog("Failed to register for remote notifications: \(error.localizedDescription)")
  }

  // MARK: - UNUserNotificationCenterDelegate

  override func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    willPresent notification: UNNotification,
    withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
  ) {
    // Present notifications while the app is in the foreground.
    completionHandler([.banner, .sound, .badge])
  }
}
