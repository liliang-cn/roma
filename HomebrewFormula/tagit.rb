class Tagit < Formula
  desc "Daemon-first orchestrator for coding-agent CLIs (claude, codex, ...)"
  homepage "https://github.com/liliang-cn/tagit"
  version "0.5.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.5.0/tagit_darwin_arm64.tar.gz"
      sha256 "2eb7a1ca21f21cfd5b513fe6ef59bc91b325443d0eca0157346f277bc69100c7"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.5.0/tagit_darwin_amd64.tar.gz"
      sha256 "0e49bdace42cb88c5e8378c9227e8bc58c918448910dbe0c7e58dc711b59d9ea"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.5.0/tagit_linux_arm64.tar.gz"
      sha256 "52eb3e9787ce2cb6259f44df660ea403e26e89536484cc14b6a17d65f8a26c6c"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.5.0/tagit_linux_amd64.tar.gz"
      sha256 "25e6fb237ef813b99221b1d0b313f7e5a90a3a518869faa4bda08b5237d15bd0"
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
