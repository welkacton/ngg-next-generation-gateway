package nodedeploy

import (
	"fmt"

	"github.com/ngg-project/ngg/internal/version"
)

// Node binary provenance.
//
// The node downloads ngg-node from this project's GitHub release, so the URL
// and the expected digest have to match the build that produced the panel.
// The release workflow compiles the linux binaries first and passes their
// SHA256 into the panel build:
//
//	-ldflags "-X github.com/ngg-project/ngg/internal/nodedeploy.NodeBinSHA256AMD64=… \
//	          -X github.com/ngg-project/ngg/internal/nodedeploy.NodeBinSHA256ARM64=…"
//
// A dev build leaves them empty; the script then says it could not verify the
// digest instead of silently claiming it did.
var (
	// NodeBinSHA256AMD64 / NodeBinSHA256ARM64 are set by the release build.
	NodeBinSHA256AMD64 = ""
	NodeBinSHA256ARM64 = ""
)

// NodeAssetName is the release asset for one GOARCH.
func NodeAssetName(arch string) string { return "ngg-node-linux-" + arch }

// DefaultNodeBinBase is the URL prefix the node appends "-<arch>" to. It is
// the exact tag of this panel build, so a panel never installs a node daemon
// from a different release.
func DefaultNodeBinBase() string {
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s/ngg-node-linux", version.Repo(), version.Release)
}

// NodeDigests returns the embedded digests for the two node architectures.
func NodeDigests() (amd64, arm64 string) { return NodeBinSHA256AMD64, NodeBinSHA256ARM64 }
