require "base64"
require "minitest/autorun"

# Execute the real lane body with a recording store boundary. A regression to
# selecting the newest build fails even when the requested build is older.
class AppStoreReleaseTest < Minitest::Test
  class Store
    UI = Class.new do
      def self.user_error!(message)
        raise ArgumentError, message
      end
    end
    attr_reader :submissions

    def initialize
      @lanes = {}
      @submissions = []
      instance_eval(File.read(File.expand_path("../../app/ios/fastlane/Fastfile", __dir__)))
    end

    def default_platform(_value); end
    def desc(_value); end
    def platform(_value); yield; end
    def lane(name, &body); @lanes[name] = body; end
    def app_store_connect_api_key(**_options); {}; end
    def latest_testflight_build_number(**_options); raise "Never choose latest for production"; end
    def deliver(**options); @submissions << options; end
    def release; @lanes.fetch(:release).call; end
  end

  def setup
    @previous = ENV.to_h
    ENV["APP_STORE_CONNECT_API_KEY_B64"] = Base64.strict_encode64("test-only")
    ENV["APP_VERSION"] = "1.0.0"
    @store = Store.new
  end

  def teardown
    ENV.replace(@previous)
  end

  def test_submits_exact_build_and_waits_for_manual_production_release
    ENV["APP_BUILD_NUMBER"] = "42"
    @store.release
    request = @store.submissions.fetch(0)
    assert_equal "42", request[:build_number]
    assert_equal "1.0.0", request[:app_version]
    assert_equal true, request[:skip_binary_upload]
    assert_equal false, request[:automatic_release]
    assert_equal true, request[:phased_release]
  end

  def test_missing_or_invalid_build_cannot_submit
    [nil, "", "0", "-1", "latest", "42\n43"].each do |number|
      ENV["APP_BUILD_NUMBER"] = number
      assert_raises(ArgumentError) { @store.release }
    end
    assert_empty @store.submissions
  end
end
