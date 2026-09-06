//go:build public_macos_arm64 && darwin && arm64 && !ios

package buildinfo

const compileTimeChannel = ChannelPublicMacOSARM64

// Release builds replace these values with go build -ldflags -X values.
var (
	version                 = ""
	licensePublicKey        = ""
	appcastURL              = ""
	revocationManifestURL   = ""
	revocationKeyID         = ""
	revocationPublicKey     = ""
	revocationBuildMetadata = ""
)
