class Zcl < Formula
  desc "Zero Context Lab (ZCL): agent evaluation harness with trace-backed evidence"
  homepage "https://github.com/marcohefti/zero-context-lab"
  url "https://codeload.github.com/marcohefti/zero-context-lab/tar.gz/refs/tags/v0.1.0"
  sha256 "29a660c2f587730dacd3ab830dbeb6fadeef8e828692425af4ba348be650d9ba"
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
