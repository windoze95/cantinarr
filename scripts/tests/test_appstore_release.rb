require "base64"
require "minitest/autorun"
require "tempfile"

# Execute the real lane body with a recording store boundary. A regression to
# selecting the newest build fails even when the requested build is older.
class AppStoreReleaseTest < Minitest::Test
  class Store
    UI = Class.new do
      def self.user_error!(message)
        raise ArgumentError, message
      end
      def self.message(_message); end
    end
    module Spaceship
      module ConnectAPI
        module App
          def self.find(identifier)
            raise "Unexpected app" unless identifier == "codes.julian.cantinarr"
            Struct.new(:id).new("cantinarr-app")
          end
        end
        module Build
          class << self
            attr_accessor :records
          end
          def self.all(app_id:, platform:, version: nil, processing_states: "PROCESSING,FAILED,INVALID,VALID", **_options)
            raise "Unexpected app" unless app_id == "cantinarr-app"
            records.select do |build|
              build.platform == platform && (version.nil? || build.app_version == version) &&
                processing_states.split(",").include?(build.processing_state)
            end
          end
        end
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
    def next_build_number; @lanes.fetch(:next_build_number).call; end
  end

  Build = Struct.new(:version, :app_version, :platform, :processing_state, :expired)

  def setup
    @previous = ENV.to_h
    ENV["APP_STORE_CONNECT_API_KEY_B64"] = Base64.strict_encode64("test-only")
    ENV["APP_VERSION"] = "1.0.0"
    @store = Store.new
    Store::Spaceship::ConnectAPI::Build.records = []
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

  def next_number(builds)
    Store::Spaceship::ConnectAPI::Build.records = builds
    Tempfile.create("testflight-build-output") do |file|
      ENV["GITHUB_OUTPUT"] = file.path
      @store.next_build_number
      file.rewind
      file.read
    end
  end

  def test_new_marketing_version_does_not_reuse_expired_equivalent_train
    # APP_VERSION is 1.0.0; Apple's older equivalent train is named 1.0.
    builds = [Build.new("447", "0.1.0", "IOS", "VALID", false),
              Build.new("1", "1.0", "IOS", "VALID", true)]
    assert_equal "build_number=448\n", next_number(builds)
  end

  def test_reserves_highest_number_regardless_of_upload_order_or_processing_state
    %w[PROCESSING FAILED INVALID VALID].each do |state|
      builds = [Build.new("8", "1.0.0", "IOS", "VALID", false),
                Build.new("600", "0.1.0", "IOS", state, true)]
      assert_equal "build_number=601\n", next_number(builds)
    end
  end

  def test_dotted_historical_build_and_other_platform
    builds = [Build.new("447", "0.1.0", "IOS", "VALID", false),
              Build.new("449.2.3", "1.0", "IOS", "VALID", true),
              Build.new("999", "1.0.0", "MAC_OS", "VALID", false)]
    assert_equal "build_number=450\n", next_number(builds)
  end

  def test_empty_build_history_starts_at_one
    assert_equal "build_number=1\n", next_number([])
  end

  def test_unreadable_build_number_cannot_allocate_an_upload
    assert_raises(ArgumentError) do
      next_number([Build.new("not-a-number", "1.0", "IOS", "VALID", false)])
    end
  end
end
