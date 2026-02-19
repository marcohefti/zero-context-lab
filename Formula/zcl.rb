class Zcl < Formula
  desc "Zero Context Lab (ZCL): agent evaluation harness with trace-backed evidence"
  homepage "https://github.com/marcohefti/zero-context-lab"
  license "MIT"
  head "https://github.com/marcohefti/zero-context-lab.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X main.version=HEAD"
    system "go", "build", "-trimpath", "-ldflags", ldflags, "-o", bin/"zcl", "./cmd/zcl"
  end

  test do
    shell_output("#{bin}/zcl version")
  end
end
