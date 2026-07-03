class Tagit < Formula
  desc "Daemon-first orchestrator for coding-agent CLIs (claude, codex, ...)"
  homepage "https://github.com/liliang-cn/tagit"
  version "0.1.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.1.0/tagit_darwin_arm64.tar.gz"
      sha256 "244fa2e096dab88cff315418e02f8ff12dcdcdd803f39472e8b61f827d53fdca"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.1.0/tagit_darwin_amd64.tar.gz"
      sha256 "495241f3eacd244a0d671bce44049ef40792127d642cf8848b1898eabdcab09f"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.1.0/tagit_linux_arm64.tar.gz"
      sha256 "d57db4c45c643b0e662ff659baf83531a60ea332e66e3346885d7c6ec1df2aa8"
    end
    on_intel do
      url "https://github.com/liliang-cn/tagit/releases/download/v0.1.0/tagit_linux_amd64.tar.gz"
      sha256 "90cc0168ad744e8e05da9c21b9e76f039e14bcdb8060836ea89d7e9bc76f8cac"
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
