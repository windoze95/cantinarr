require "fileutils"
require "tmpdir"
require "minitest/autorun"

ENV["FASTLANE_SKIP_UPDATE_CHECK"] = "1"
ENV["FASTLANE_SKIP_ACTION_SUMMARY"] = "1"
ENV["FASTLANE_HIDE_TIMESTAMP"] = "1"
ENV["FASTLANE_OPT_OUT_USAGE"] = "1"

require "fastlane"
require "supply"
require "supply/options"
require "supply/client"
Fastlane.load_actions

# Run the actual Fastfile and Supply uploader against an in-memory Play client.
# No credentials, Android SDK, bundle parsing, or network calls are needed.
class PlayReleaseTest < Minitest::Test
  FASTFILE = File.expand_path("../../app/android/fastlane/Fastfile", __dir__)
  VERSION_CODE = 244

  class PlayClient
    attr_reader :published, :bundle_uploads, :commits, :update_attempts
    attr_accessor :fail_track, :extra_alpha_release

    def initialize
      @published = { "alpha" => release(242), "beta" => release(243), "internal" => release(7) }
      @bundle_uploads = 0
      @commits = 0
      @update_attempts = []
    end

    def release(code)
      Google::Apis::AndroidpublisherV3::Track.new(releases: [
        Google::Apis::AndroidpublisherV3::TrackRelease.new(version_codes: [code.to_s], status: "completed")
      ])
    end

    def begin_edit(package_name:)
      raise "Unexpected package" unless package_name == "codes.julian.cantinarr"

      @edit = Marshal.load(Marshal.dump(@published))
    end

    def upload_bundle(_path)
      @bundle_uploads += 1
      raise "Duplicate bundle upload" if @bundle_uploads > 1

      VERSION_CODE
    end

    def tracks(name)
      @edit[name] ? [Marshal.load(Marshal.dump(@edit[name]))] : []
    end

    def track_version_codes(name)
      tracks(name).flat_map(&:releases).flat_map(&:version_codes).map(&:to_i)
    end

    def abort_current_edit
      @edit = nil
    end

    def update_track(name, track)
      @update_attempts << name
      raise "Play rejected #{name}" if name == fail_track

      # The Play API returns version codes as strings.
      track.releases.each { |release| release.version_codes.map!(&:to_s) }
      @edit[name] = track
    end

    def upload_changelogs(track, name)
      update_track(name, track)
    end

    def listing_for_language(language)
      Google::Apis::AndroidpublisherV3::Listing.new(language: language)
    end

    def commit_current_edit!
      @published = @edit
      @commits += 1
      if extra_alpha_release && @commits == 1
        @published["alpha"].releases.concat(release(999).releases)
      end
    end
  end

  def setup
    @previous_env = ENV.to_h
    @play = PlayClient.new
    play = @play
    Supply::Client.define_singleton_method(:make_from_config) { |**_options| play }
    @tmp = Dir.mktmpdir("cantinarr-play-release-")
    @android = File.join(@tmp, "android")
    notes = File.join(@android, "fastlane/metadata/android/en-US/changelogs")
    FileUtils.mkdir_p(notes)
    File.write(File.join(notes, "default.txt"), "Release notes for this build")
    bundle = File.join(@tmp, "build/app/outputs/bundle/release/app-release.aab")
    FileUtils.mkdir_p(File.dirname(bundle))
    File.write(bundle, "Bundle bytes are handled by the fake Play client")
    %w[PLAY_TRACK PLAY_VERSION_CODE PLAY_RELEASE_STATUS PLAY_JSON_KEY_PATH PLAY_OWNER_CANDIDATE].each { |key| ENV.delete(key) }
  end

  def teardown
    ENV.replace(@previous_env) if @previous_env
    FileUtils.remove_entry(@tmp) if @tmp
  end

  def publish(track: nil, status: "completed", version_code: VERSION_CODE.to_s)
    ENV["PLAY_TRACK"] = track
    ENV["PLAY_RELEASE_STATUS"] = status
    ENV["PLAY_VERSION_CODE"] = version_code
    Dir.chdir(@android) do
      # Reuse the parsed lane definitions; every action still executes normally.
      capture_subprocess_io do
        @@fastfile ||= Fastlane::FastFile.new.parse(File.read(FASTFILE))
        @@fastfile.runner.execute(:beta, :android)
      end
    end
  end

  def assert_release(track, code: VERSION_CODE, status: "completed")
    release = @play.published.fetch(track).releases.first
    assert_equal [code.to_s], release.version_codes
    assert_equal status, release.status
    release
  end

  def test_default_publishes_one_bundle_and_its_notes_to_both_tracks
    publish
    alpha = assert_release("alpha")
    beta = assert_release("beta")
    assert_equal "Release notes for this build", alpha.release_notes.first.text
    assert_equal alpha.release_notes.map(&:to_h), beta.release_notes.map(&:to_h)
    assert_release("internal", code: 7)
    assert_equal 1, @play.bundle_uploads
    assert_equal 2, @play.commits
  end

  def test_explicit_both_keeps_draft_status_on_both_tracks
    publish(track: "both", status: "draft")
    assert_release("alpha", status: "draft")
    assert_release("beta", status: "draft")
    assert_equal 1, @play.bundle_uploads
  end

  def test_candidate_reaches_owner_and_both_public_tracks_from_one_upload
    ENV["PLAY_OWNER_CANDIDATE"] = "true"
    publish
    %w[alpha beta internal].each do |track|
      release = assert_release(track)
      assert_equal "Release notes for this build", release.release_notes.first.text
    end
    assert_equal 1, @play.bundle_uploads
    assert_equal 3, @play.commits
  end

  def test_candidate_cannot_skip_public_testing
    ENV["PLAY_OWNER_CANDIDATE"] = "true"
    assert_raises(FastlaneCore::Interface::FastlaneError) { publish(track: "internal") }
    assert_equal 0, @play.bundle_uploads
  end

  %w[alpha beta internal].each do |track|
    define_method("test_explicit_#{track}_leaves_other_tracks_untouched") do
      others = @play.published.reject { |name, _| name == track }
      before = Marshal.dump(others)
      publish(track: track, version_code: nil)
      assert_release(track)
      assert_equal before, Marshal.dump(@play.published.reject { |name, _| name == track })
      assert_equal 1, @play.bundle_uploads
      assert_equal 1, @play.commits
    end
  end

  def test_promotion_selects_this_build_when_alpha_has_multiple_releases
    @play.extra_alpha_release = true
    publish
    assert_release("beta")
    assert_equal 1, @play.bundle_uploads
  end

  def test_rejects_invalid_targets_before_uploading
    ["", "production", "typo"].each do |track|
      error = assert_raises(FastlaneCore::Interface::FastlaneError) { publish(track: track) }
      assert_match(/PLAY_TRACK must be/, error.message)
    end
    assert_equal 0, @play.bundle_uploads
    assert_equal 0, @play.commits
  end

  def test_requires_an_exact_positive_build_number_before_uploading_to_both
    [nil, "", "0", "-1", "244oops"].each do |code|
      error = assert_raises(FastlaneCore::Interface::FastlaneError) { publish(version_code: code) }
      assert_match(/PLAY_VERSION_CODE must be/, error.message)
    end
    assert_equal 0, @play.bundle_uploads
    assert_equal 0, @play.commits
  end

  def test_a_mismatched_build_number_fails_instead_of_promoting_another_release
    error = assert_raises(FastlaneCore::Interface::FastlaneError) { publish(version_code: "999") }
    assert_match(/doesn't have any releases/, error.message)
    assert_release("beta", code: 243)
    assert_equal 1, @play.commits
  end

  def test_a_failed_closed_upload_does_not_attempt_open_testing
    @play.fail_track = "alpha"
    error = assert_raises(RuntimeError) { publish }
    assert_equal "Play rejected alpha", error.message
    assert_equal ["alpha"], @play.update_attempts
    assert_release("alpha", code: 242)
    assert_release("beta", code: 243)
    assert_equal 0, @play.commits
  end

  def test_a_failed_open_promotion_fails_the_lane_after_closed_testing_was_updated
    @play.fail_track = "beta"
    error = assert_raises(RuntimeError) { publish }
    assert_equal "Play rejected beta", error.message
    assert_release("alpha")
    assert_release("beta", code: 243)
    assert_equal 1, @play.bundle_uploads
    assert_equal 1, @play.commits
  end

  def promote_to_production(code = VERSION_CODE.to_s)
    ENV["PLAY_VERSION_CODE"] = code
    Dir.chdir(@android) do
      capture_subprocess_io do
        @@fastfile ||= Fastlane::FastFile.new.parse(File.read(FASTFILE))
        @@fastfile.runner.execute(:release, :android)
      end
    end
  end

  def test_production_uses_selected_candidate_without_another_bundle_upload
    @play.published["alpha"] = @play.release(VERSION_CODE)
    promote_to_production
    assert_release("production")
    assert_equal 0, @play.bundle_uploads
  end

  def test_production_retry_is_a_no_op
    @play.published["production"] = @play.release(VERSION_CODE)
    promote_to_production
    assert_empty @play.update_attempts
    assert_equal 0, @play.bundle_uploads
  end

  def test_old_release_cannot_replace_newer_production
    @play.published["production"] = @play.release(VERSION_CODE + 1)
    assert_raises(FastlaneCore::Interface::FastlaneError) { promote_to_production }
    assert_empty @play.update_attempts
  end

  def test_missing_candidate_is_not_replaced_with_latest
    assert_raises(FastlaneCore::Interface::FastlaneError) { promote_to_production("999") }
    assert_empty @play.update_attempts
    assert_equal 0, @play.bundle_uploads
  end

  def test_production_requires_exact_version_code
    [nil, "", "latest", "0", "42\n43"].each do |value|
      assert_raises(FastlaneCore::Interface::FastlaneError) { promote_to_production(value) }
    end
    assert_empty @play.update_attempts
  end
end
