class Tagit < Formula
  desc "Daemon-first orchestrator for coding-agent CLIs (claude, codex, ...)"
  homepage "https://github.com/liliang-cn/tagit"
  version "0.4.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.4.0/tagit_darwin_arm64.tar.gz"
      sha256 "231089a89ef96d6d5f4d62f8a88379a2bed933659399d27ffc56f940deaf2b18"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.4.0/tagit_darwin_amd64.tar.gz"
      sha256 "36aed50bc08cfed3854d09cac740cb7ea9398f8c93d0f47177c0f13513925a4e"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.4.0/tagit_linux_arm64.tar.gz"
      sha256 "158bbbd709d99bca80f61775a4494fe6a795d48368ff2bfa90a7c8f81089eb64"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.4.0/tagit_linux_amd64.tar.gz"
      sha256 "94fa7b771e9192ace0601462151155a40ae2a17e8bde6cbc8ce4347a00ae3ed3"
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
