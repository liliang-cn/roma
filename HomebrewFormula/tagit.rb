class Tagit < Formula
  desc "Daemon-first orchestrator for coding-agent CLIs (claude, codex, ...)"
  homepage "https://github.com/liliang-cn/tagit"
  version "0.3.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.3.0/tagit_darwin_arm64.tar.gz"
      sha256 "0a77804e7c1a0eb3baa83abc9742c6c27ab41a3b80c3ed8f954010d551ddc940"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.3.0/tagit_darwin_amd64.tar.gz"
      sha256 "5ceb9f5465625b69916ee077b9e2f523c7fee34f11a622f8d7e80e2643b27d06"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.3.0/tagit_linux_arm64.tar.gz"
      sha256 "d606949405c612bd2d23876eb69eca86438cf7e1a69cf39e00b4573efe271ab4"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.3.0/tagit_linux_amd64.tar.gz"
      sha256 "859435e35a63cf2981fb3bdcb15e8cfd094ac867c7ff6a0015c1229deb587245"
    end
  end

  # Prebuilt binaries — no Go toolchain required.
  def install
    bin.install "tagit", "tagitd"
  end

  # `brew services start tagit` runs the daemon on login and keeps it alive.
  # The daemon uses ~/.tagit for state and config (agents.json, feishu.json, slack.json).
  service do
    run [opt_bin/"tagitd"]
    keep_alive true
    log_path var/"log/tagit.log"
    error_log_path var/"log/tagit.log"
  end

  test do
    assert_match "tagit usage", shell_output("#{bin}/tagit --help")
  end
end
