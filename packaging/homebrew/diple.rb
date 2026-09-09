# Homebrew formula for diple.
#
# The release boundary (S-011) fills in VERSION, the release URLs, and their
# sha256 sums when the first release is cut; until then this formula is the
# shape the tap will carry, not a working install.
class Diple < Formula
  desc "Transparent terminal layer for pointing at an AI coding agent's output"
  homepage "https://github.com/maximalfocus/diple"
  version "0.0.0" # set at the release boundary
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/maximalfocus/diple/releases/download/v#{version}/diple_#{version}_darwin_arm64.tar.gz"
      sha256 "0" * 64 # set at the release boundary
    end
    on_intel do
      url "https://github.com/maximalfocus/diple/releases/download/v#{version}/diple_#{version}_darwin_amd64.tar.gz"
      sha256 "0" * 64 # set at the release boundary
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/maximalfocus/diple/releases/download/v#{version}/diple_#{version}_linux_arm64.tar.gz"
      sha256 "0" * 64 # set at the release boundary
    end
    on_intel do
      url "https://github.com/maximalfocus/diple/releases/download/v#{version}/diple_#{version}_linux_amd64.tar.gz"
      sha256 "0" * 64 # set at the release boundary
    end
  end

  def install
    bin.install "diple"
  end

  def caveats
    <<~EOS
      Run `diple on` to install the shims and put them on your PATH, then
      re-read your profile. `diple status` says what is in place, `DIPLE=0`
      bypasses Diple for one command, and `diple off` removes everything.
    EOS
  end

  test do
    assert_match "usage: diple", shell_output("#{bin}/diple 2>&1", 64)
  end
end
