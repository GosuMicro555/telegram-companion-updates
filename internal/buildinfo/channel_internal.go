//go:build !public_macos_arm64

package buildinfo

const compileTimeChannel = ChannelInternal

var (
	version                 = "dev"
	licensePublicKey        = ""
	appcastURL              = ""
	revocationManifestURL   = ""
	revocationKeyID         = ""
	revocationPublicKey     = ""
	revocationBuildMetadata = ""
)
