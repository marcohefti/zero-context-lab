class Zcl < Formula
  desc "Zero Context Lab (ZCL): agent evaluation harness with trace-backed evidence"
  homepage "https://github.com/marcohefti/zero-context-lab"
  url "https://codeload.github.com/marcohefti/zero-context-lab/tar.gz/refs/tags/v0.1.1"
  sha256 "e669b8a1f09532786f58207837301f17ebd313352b66d5e75013015112b84956"
  license "MIT"
  head "https://github.com/marcohefti/zero-context-lab.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X main.version=v#{version}"
    system "go", "build", "-trimpath", "-ldflags", ldflags, "-o", bin/"zcl", "./cmd/zcl"
  end

  test do
    assert_match "v#{version}", shell_output("#{bin}/zcl version").strip
  end
end
