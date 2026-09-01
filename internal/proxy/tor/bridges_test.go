package tor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadBridgeBundleAcceptsValidObfs4AndRejectsInjection(t *testing.T) {
	path := writeOfficialShapeFixture(t, []string{"obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA== iat-mode=0"}, nil)
	candidates, err := LoadBridgeBundle(path)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, TransportObfs4, candidates[0].Transport)
	require.Len(t, candidates[0].ID, 64)

	_, err = LoadBridgeBundle(writeOfficialShapeFixture(t, []string{"obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA== iat-mode=0\nSocksPort 0"}, nil))
	require.ErrorContains(t, err, "bridge configuration is invalid")
}

func TestLoadBridgeBundleAcceptsTorBrowser15020MacOSMappings(t *testing.T) {
	contents := `{"recommendedDefault":"obfs4","pluggableTransports":{"lyrebird":"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird","snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"meek":[],"obfs4":["obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA iat-mode=0"],"snowflake":["snowflake 192.0.2.3:80 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=2B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid fronts=example.invalid ice=stun:example.invalid:3478 utls-imitate=hellorandomizedalpn"]}}`
	candidates, err := LoadBridgeBundle(writeBridgeFixture(t, contents))
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Equal(t, TransportObfs4, candidates[0].Transport)
	require.Equal(t, TransportSnowflake, candidates[1].Transport)
}

func TestLoadBridgeBundleRejectsPrivateEndpointAndBadFingerprint(t *testing.T) {
	for _, line := range []string{
		"obfs4 127.0.0.1:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA== iat-mode=0",
		"obfs4 203.0.113.10:443 not-a-fingerprint cert=YWJjZA== iat-mode=0",
	} {
		_, err := LoadBridgeBundle(writeOfficialShapeFixture(t, []string{line}, nil))
		require.Error(t, err)
	}
}

func TestLoadBridgeBundleAcceptsReservedSnowflakeEndpoint(t *testing.T) {
	candidates, err := LoadBridgeBundle(writeOfficialShapeFixture(t, nil, []string{"snowflake 192.0.2.3:80 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=2B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid front=example.invalid ice=stun:example.invalid:3478 utls-imitate=hellorandomizedalpn"}))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, TransportSnowflake, candidates[0].Transport)
}

func TestLoadBridgeBundleAcceptsOfficialSchemaMetadataRawCertAndFronts(t *testing.T) {
	candidates, err := LoadBridgeBundle(writeOfficialShapeFixture(t,
		[]string{"obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA iat-mode=0"},
		[]string{"snowflake 192.0.2.3:80 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=2B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid fronts=example.invalid ice=stun:example.invalid:3478 utls-imitate=hellorandomizedalpn"},
	))
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Equal(t, TransportObfs4, candidates[0].Transport)
	require.Equal(t, TransportSnowflake, candidates[1].Transport)
}

func TestLoadBridgeBundleBoundsOfficialNineCandidateSetAndKeepsSnowflakeFallback(t *testing.T) {
	obfs4 := func(last byte) string {
		fingerprint := "ABCDEF0123456789ABCDEF0123456789ABCDEF0" + string(last)
		return "obfs4 203.0.113.10:443 " + fingerprint + " cert=YWJjZA iat-mode=0"
	}
	obfsLines := []string{obfs4('1'), obfs4('2'), obfs4('3'), obfs4('4'), obfs4('5'), obfs4('6'), obfs4('7')}
	snowLines := []string{
		"snowflake 192.0.2.3:80 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=2B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid fronts=example.invalid ice=stun:example.invalid:3478 utls-imitate=hellorandomizedalpn",
		"snowflake 192.0.2.4:80 3B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=3B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid fronts=example.invalid ice=stun:example.invalid:3478 utls-imitate=hellorandomizedalpn",
	}
	candidates, err := LoadBridgeBundle(writeOfficialShapeFixture(t, obfsLines, snowLines))
	require.NoError(t, err)
	require.Len(t, candidates, 9)
	require.Equal(t, bridgeID(snowLines[1]), candidates[8].ID)
	require.Equal(t, TransportSnowflake, candidates[8].Transport)
}

func TestLoadBridgeBundleRejectsMalformedTailAfterCandidateCap(t *testing.T) {
	validObfs4 := func(last byte) string {
		fingerprint := "ABCDEF0123456789ABCDEF0123456789ABCDEF0" + string(last)
		return "obfs4 203.0.113.10:443 " + fingerprint + " cert=YWJjZA iat-mode=0"
	}
	obfsLines := []string{validObfs4('1'), validObfs4('2'), validObfs4('3'), validObfs4('4'), validObfs4('5'), validObfs4('6'), validObfs4('7'), validObfs4('8')}
	_, err := LoadBridgeBundle(writeOfficialShapeFixture(t, append(obfsLines, "obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF09 cert=not-base64 iat-mode=0"), nil))
	require.Error(t, err)
}

func TestLoadBridgeBundleRejectsUnknownFieldsAndMalformedBase64(t *testing.T) {
	baseObfs4 := `"obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA iat-mode=0"`
	for _, contents := range []string{
		`{"unexpected":true,"bridges":{"obfs4":[` + baseObfs4 + `]}}`,
		`{"pluggableTransports":{"lyrebird":"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird","snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"obfs4":["obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJj$A iat-mode=0"]}}`,
		`{"pluggableTransports":{"lyrebird":"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird","snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"snowflake":["snowflake 192.0.2.3:80 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=2B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid fronts=example.invalid ice=stun:example.invalid:3478 unknown=value"]}}`,
	} {
		_, err := LoadBridgeBundle(writeBridgeFixture(t, contents))
		require.Error(t, err)
	}
}

func TestLoadBridgeBundleRejectsWhenNoSupportedCandidatesRemain(t *testing.T) {
	_, err := LoadBridgeBundle(writeOfficialShapeFixture(t, nil, nil))
	require.Error(t, err)
}

func TestLoadBridgeBundleRejectsInvalidBridgePorts(t *testing.T) {
	for index, fixture := range []string{
		`{"pluggableTransports":{"lyrebird":"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird","snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"obfs4":["obfs4 203.0.113.10: ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA iat-mode=0"]}}`,
		`{"pluggableTransports":{"lyrebird":"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird","snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"obfs4":["obfs4 203.0.113.10:0 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA iat-mode=0"]}}`,
		`{"pluggableTransports":{"lyrebird":"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird","snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"obfs4":["obfs4 203.0.113.10:65536 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA iat-mode=0"]}}`,
		`{"pluggableTransports":{"lyrebird":"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird","snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"snowflake":["snowflake 192.0.2.3:99999 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=2B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid fronts=example.invalid ice=stun:example.invalid:3478 utls-imitate=hellorandomizedalpn"]}}`,
	} {
		t.Run(fmt.Sprintf("fixture-%d", index), func(t *testing.T) {
			_, err := LoadBridgeBundle(writeBridgeFixture(t, fixture))
			require.Error(t, err, fixture)
		})
	}
}

func TestLoadBridgeBundleRejectsSnowflakeFingerprintMismatch(t *testing.T) {
	line := "snowflake 192.0.2.3:80 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=3B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid fronts=example.invalid ice=stun:example.invalid:3478 utls-imitate=hellorandomizedalpn"
	_, err := LoadBridgeBundle(writeOfficialShapeFixture(t, nil, []string{line}))
	require.Error(t, err)
}

func TestLoadBridgeBundleResolvesOfficialSnowflakeMappingToLyrebird(t *testing.T) {
	candidates, err := LoadBridgeBundle(writeOfficialShapeFixture(t, sevenObfs4Lines(), twoSnowflakeLines()))
	require.NoError(t, err)
	require.Len(t, candidates, 9)
	for _, candidate := range candidates {
		require.Equal(t, ManagedPTLyrebird, candidate.Program)
	}
	require.Equal(t, TransportSnowflake, candidates[7].Transport)
	require.Equal(t, TransportSnowflake, candidates[8].Transport)
}

func TestLoadBridgeBundleRejectsUnsupportedMappingWithoutLeak(t *testing.T) {
	const sentinel = "SENSITIVE-MAPPING-SENTINEL"
	_, err := LoadBridgeBundle(writeMappingFixture(t, sentinel))
	require.ErrorIs(t, err, errBridgeConfigurationInvalid)
	require.NotContains(t, err.Error(), sentinel)
}

func sevenObfs4Lines() []string {
	obfs4 := func(last byte) string {
		fingerprint := "ABCDEF0123456789ABCDEF0123456789ABCDEF0" + string(last)
		return "obfs4 203.0.113.10:443 " + fingerprint + " cert=YWJjZA iat-mode=0"
	}
	return []string{obfs4('1'), obfs4('2'), obfs4('3'), obfs4('4'), obfs4('5'), obfs4('6'), obfs4('7')}
}

func twoSnowflakeLines() []string {
	return []string{
		"snowflake 192.0.2.3:80 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=2B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid fronts=example.invalid ice=stun:example.invalid:3478 utls-imitate=hellorandomizedalpn",
		"snowflake 192.0.2.4:80 3B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=3B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://example.invalid fronts=example.invalid ice=stun:example.invalid:3478 utls-imitate=hellorandomizedalpn",
	}
}

func writeOfficialShapeFixture(t *testing.T, obfs4, snowflake []string) string {
	t.Helper()
	contents := fmt.Sprintf(`{"recommendedDefault":"obfs4","pluggableTransports":{"lyrebird":"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird","snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"meek":["ignored by this app"],"obfs4":%s,"snowflake":%s}}`, jsonStringSlice(obfs4), jsonStringSlice(snowflake))
	return writeBridgeFixture(t, contents)
}

func writeMappingFixture(t *testing.T, mapping string) string {
	t.Helper()
	contents := fmt.Sprintf(`{"pluggableTransports":{"lyrebird":%q,"snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"obfs4":["obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA iat-mode=0"]}}`, mapping)
	return writeBridgeFixture(t, contents)
}

func jsonStringSlice(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = fmt.Sprintf("%q", value)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func writeBridgeFixture(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "bridges.json")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
