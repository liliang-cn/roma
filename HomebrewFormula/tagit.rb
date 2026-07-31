class Tagit < Formula
  desc "Daemon-first orchestrator for coding-agent CLIs (claude, codex, ...)"
  homepage "https://github.com/liliang-cn/tagit"
  version "0.2.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.2.0/tagit_darwin_arm64.tar.gz"
      sha256 "9a4d778366db517ec1bb8900acf7005091a062054850c4564b4f1c232a98d7a5"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.2.0/tagit_darwin_amd64.tar.gz"
      sha256 "fe831550023f280c5c047e89c99b9237d2022707cdafe92fdff62792f3a5fa25"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.2.0/tagit_linux_arm64.tar.gz"
      sha256 "d9a3b9acd9a9fd6f4593207f013dea501f4b80dca0498917b2b6165bbabd626a"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.2.0/tagit_linux_amd64.tar.gz"
      sha256 "f33d9fb79d0438fa4543c97ef41acad05080ff06a72290e58a619bfd026a5644"
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
