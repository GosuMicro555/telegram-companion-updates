package license

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"sync"

	"telegram-companion/internal/buildinfo"
	"telegram-companion/internal/revocation"
)

// GateState is the externally visible state of the process license gate.
type GateState string

const (
	StateChecking        GateState = "checking"
	StateNeedsActivation GateState = "needs_activation"
	StateActivated       GateState = "activated"
	StateError           GateState = "error"
	StateRevoked         GateState = "revoked"
	StateCheckRequired   GateState = "check_required"
)

// Gate-level error codes classify failures that do not come from the license
// codec. They deliberately carry no dependency error text.
const (
	GateErrorConfiguration    = "configuration_error"
	GateErrorMalformedLicense = "malformed_license"
	GateErrorStorage          = "storage_error"
	GateErrorVerification     = "verification_error"
	GateErrorRevoked          = "license_revoked"
	GateErrorCheckRequired    = "revocation_check_required"
)

// Snapshot is an immutable-by-copy view of the gate state.
type Snapshot struct {
	Mode      buildinfo.Channel `json:"mode"`
	State     GateState         `json:"state"`
	MachineID string            `json:"machineID,omitempty"`
	ErrorCode string            `json:"errorCode,omitempty"`
}

// TokenStore loads and atomically saves a license token.
type TokenStore interface {
	Load() (string, error)
	Save(string) error
}

// MachineIDProvider returns the derived, non-raw Machine ID.
type MachineIDProvider func() (string, error)

// Verifier verifies a license token in the supplied build and machine scope.
type Verifier func(string, VerifyOptions) (Payload, error)

// Gate owns the synchronized process-level license decision.
type Gate struct {
	operations sync.Mutex
	snapshots  sync.RWMutex

	info                buildinfo.Info
	store               TokenStore
	machineID           MachineIDProvider
	verify              Verifier
	revocationChecker   revocation.DecisionChecker
	revocationRequired  bool
	authorizedLicenseID string
	snapshot            Snapshot
}

// NewGate constructs a gate using the platform Machine ID and canonical
// license verifier. Public gates perform their startup check before returning.
func NewGate(info buildinfo.Info, store TokenStore) *Gate {
	return NewGateWithDependencies(info, store, MachineID, ParseAndVerify)
}

// NewGateWithDependencies constructs a gate with deterministic dependencies.
func NewGateWithDependencies(
	info buildinfo.Info,
	store TokenStore,
	machineID MachineIDProvider,
	verify Verifier,
) *Gate {
	return newGateWithDependencies(info, store, machineID, verify, nil, false)
}

// NewGateWithRevocationDependencies constructs a public gate that requires a
// high-level revocation decision after local license verification.
func NewGateWithRevocationDependencies(
	info buildinfo.Info,
	store TokenStore,
	machineID MachineIDProvider,
	verify Verifier,
	checker revocation.DecisionChecker,
) *Gate {
	return newGateWithDependencies(info, store, machineID, verify, checker, true)
}

func newGateWithDependencies(
	info buildinfo.Info,
	store TokenStore,
	machineID MachineIDProvider,
	verify Verifier,
	checker revocation.DecisionChecker,
	revocationRequired bool,
) *Gate {
	gate := &Gate{
		info:               info,
		store:              store,
		machineID:          machineID,
		verify:             verify,
		revocationChecker:  checker,
		revocationRequired: revocationRequired,
		snapshot:           Snapshot{Mode: info.Channel, State: StateChecking},
	}

	switch info.Channel {
	case buildinfo.ChannelInternal:
		gate.publish(Snapshot{Mode: info.Channel, State: StateActivated})
	case buildinfo.ChannelPublicMacOSARM64:
		gate.Check()
	default:
		gate.publish(gate.errorSnapshot("", GateErrorConfiguration))
	}
	return gate
}

// Snapshot returns a detached value that callers may safely retain or mutate.
func (g *Gate) Snapshot() Snapshot {
	g.snapshots.RLock()
	defer g.snapshots.RUnlock()
	return g.snapshot
}

// Authorized reports whether the complete application runtime may be built.
func (g *Gate) Authorized() bool {
	return g.Snapshot().State == StateActivated
}

// AuthorizedLicenseID returns the locally verified License ID only while the
// gate is active. The identifier is never included in Snapshot or UI DTOs.
func (g *Gate) AuthorizedLicenseID() (string, bool) {
	g.operations.Lock()
	defer g.operations.Unlock()
	if g.Snapshot().State != StateActivated || g.authorizedLicenseID == "" {
		return "", false
	}
	return g.authorizedLicenseID, true
}

// ApplyRevocationDecision atomically closes an active public gate for a
// terminal supervisor decision. Nonterminal decisions cannot change authority.
func (g *Gate) ApplyRevocationDecision(decision revocation.Decision) Snapshot {
	g.operations.Lock()
	defer g.operations.Unlock()
	if g.info.Channel != buildinfo.ChannelPublicMacOSARM64 {
		return g.Snapshot()
	}
	var snapshot Snapshot
	switch decision {
	case revocation.Revoked:
		snapshot = Snapshot{Mode: g.info.Channel, State: StateRevoked, MachineID: g.Snapshot().MachineID, ErrorCode: GateErrorRevoked}
	case revocation.CheckRequired:
		snapshot = Snapshot{Mode: g.info.Channel, State: StateCheckRequired, MachineID: g.Snapshot().MachineID, ErrorCode: GateErrorCheckRequired}
	default:
		return g.Snapshot()
	}
	g.authorizedLicenseID = ""
	return g.publish(snapshot)
}

// SeedGrant re-verifies the stored Public license and returns its universal
// bootstrap grant. Internal builds never consume a Public bootstrap bundle.
func (g *Gate) SeedGrant() (SeedGrant, error) {
	g.operations.Lock()
	defer g.operations.Unlock()

	if g.info.Channel != buildinfo.ChannelPublicMacOSARM64 {
		return SeedGrant{}, ErrNoSeedGrant
	}
	options, errorCode := g.verifyOptions()
	if errorCode != "" || g.store == nil || g.verify == nil {
		return SeedGrant{}, ErrMalformed
	}
	token, err := g.store.Load()
	if err != nil {
		return SeedGrant{}, err
	}
	payload, err := g.verify(token, options)
	if err != nil {
		return SeedGrant{}, err
	}
	return payload.SeedGrant()
}

// Check re-evaluates the stored license for the current build and machine.
func (g *Gate) Check() Snapshot {
	g.operations.Lock()
	defer g.operations.Unlock()

	switch g.info.Channel {
	case buildinfo.ChannelInternal:
		return g.publish(Snapshot{Mode: g.info.Channel, State: StateActivated})
	case buildinfo.ChannelPublicMacOSARM64:
		g.authorizedLicenseID = ""
		g.publishChecking()
	default:
		return g.publish(g.errorSnapshot("", GateErrorConfiguration))
	}

	options, errorCode := g.verifyOptions()
	if errorCode != "" {
		return g.publish(g.errorSnapshot(options.MachineID, errorCode))
	}
	if g.store == nil {
		return g.publish(g.errorSnapshot(options.MachineID, GateErrorStorage))
	}

	token, err := g.store.Load()
	if errors.Is(err, ErrNotFound) {
		return g.publish(Snapshot{
			Mode:      g.info.Channel,
			State:     StateNeedsActivation,
			MachineID: options.MachineID,
		})
	}
	if err != nil {
		return g.publish(g.errorSnapshot(options.MachineID, storageErrorCode(err)))
	}
	if g.verify == nil {
		return g.publish(g.errorSnapshot(options.MachineID, GateErrorVerification))
	}
	payload, err := g.verify(token, options)
	if err != nil {
		return g.publish(g.errorSnapshot(options.MachineID, verificationErrorCode(err)))
	}
	snapshot := g.revocationSnapshot(payload, options.MachineID, true)
	if snapshot.State == StateActivated {
		g.authorizedLicenseID = payload.LicenseID
	}
	return g.publish(snapshot)
}

// Activate verifies token before asking the store to atomically persist it.
func (g *Gate) Activate(token string) Snapshot {
	g.operations.Lock()
	defer g.operations.Unlock()

	switch g.info.Channel {
	case buildinfo.ChannelInternal:
		return g.publish(Snapshot{Mode: g.info.Channel, State: StateActivated})
	case buildinfo.ChannelPublicMacOSARM64:
		g.authorizedLicenseID = ""
		g.publishChecking()
	default:
		return g.publish(g.errorSnapshot("", GateErrorConfiguration))
	}

	options, errorCode := g.verifyOptions()
	if errorCode != "" {
		return g.publish(g.errorSnapshot(options.MachineID, errorCode))
	}
	if g.verify == nil {
		return g.publish(g.errorSnapshot(options.MachineID, GateErrorVerification))
	}
	payload, err := g.verify(token, options)
	if err != nil {
		return g.publish(g.errorSnapshot(options.MachineID, verificationErrorCode(err)))
	}
	revocationSnapshot := g.revocationSnapshot(payload, options.MachineID, false)
	if revocationSnapshot.State != StateActivated {
		return g.publish(revocationSnapshot)
	}
	if g.store == nil {
		return g.publish(g.errorSnapshot(options.MachineID, GateErrorStorage))
	}
	if err := g.store.Save(token); err != nil {
		return g.publish(g.errorSnapshot(options.MachineID, GateErrorStorage))
	}
	g.authorizedLicenseID = payload.LicenseID
	return g.publish(Snapshot{
		Mode:      g.info.Channel,
		State:     StateActivated,
		MachineID: options.MachineID,
	})
}

func (g *Gate) revocationSnapshot(payload Payload, machineID string, allowGrace bool) Snapshot {
	active := Snapshot{Mode: g.info.Channel, State: StateActivated, MachineID: machineID}
	if !g.revocationRequired {
		return active
	}
	if g.revocationChecker == nil {
		return g.errorSnapshot(machineID, GateErrorConfiguration)
	}
	decision, err := g.revocationChecker.Check(context.Background(), payload.LicenseID)
	if err != nil {
		return Snapshot{Mode: g.info.Channel, State: StateCheckRequired, MachineID: machineID, ErrorCode: GateErrorCheckRequired}
	}
	switch decision {
	case revocation.Active:
		return active
	case revocation.ActiveInGrace:
		if allowGrace {
			return active
		}
		return Snapshot{Mode: g.info.Channel, State: StateCheckRequired, MachineID: machineID, ErrorCode: GateErrorCheckRequired}
	case revocation.Revoked:
		return Snapshot{Mode: g.info.Channel, State: StateRevoked, MachineID: machineID, ErrorCode: GateErrorRevoked}
	case revocation.CheckRequired:
		return Snapshot{Mode: g.info.Channel, State: StateCheckRequired, MachineID: machineID, ErrorCode: GateErrorCheckRequired}
	default:
		return Snapshot{Mode: g.info.Channel, State: StateCheckRequired, MachineID: machineID, ErrorCode: GateErrorCheckRequired}
	}
}

func (g *Gate) verifyOptions() (VerifyOptions, string) {
	var options VerifyOptions
	if g.info.ProductID != buildinfo.ProductID {
		return options, GateErrorConfiguration
	}
	if g.machineID == nil {
		return options, string(CodeMachineUnavailable)
	}

	machineID, err := g.machineID()
	if err != nil || machineID == "" {
		return options, string(CodeMachineUnavailable)
	}
	options = VerifyOptions{
		Product:   g.info.ProductID,
		Channel:   string(g.info.Channel),
		MachineID: machineID,
	}

	publicKey, err := base64.StdEncoding.DecodeString(g.info.LicensePublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize ||
		base64.StdEncoding.EncodeToString(publicKey) != g.info.LicensePublicKey {
		return options, string(CodeInvalidPublicKey)
	}
	options.PublicKey = ed25519.PublicKey(publicKey)
	return options, ""
}

func (g *Gate) publishChecking() {
	g.snapshots.Lock()
	defer g.snapshots.Unlock()
	g.snapshot = Snapshot{
		Mode:      g.info.Channel,
		State:     StateChecking,
		MachineID: g.snapshot.MachineID,
	}
}

func (g *Gate) publish(snapshot Snapshot) Snapshot {
	g.snapshots.Lock()
	g.snapshot = snapshot
	g.snapshots.Unlock()
	return snapshot
}

func (g *Gate) errorSnapshot(machineID, code string) Snapshot {
	return Snapshot{
		Mode:      g.info.Channel,
		State:     StateError,
		MachineID: machineID,
		ErrorCode: code,
	}
}

func storageErrorCode(err error) string {
	if errors.Is(err, ErrMalformed) {
		return GateErrorMalformedLicense
	}
	return GateErrorStorage
}

func verificationErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrMalformed):
		return GateErrorMalformedLicense
	case errors.Is(err, ErrUnsupportedSchema):
		return string(CodeUnsupportedSchema)
	case errors.Is(err, ErrInvalidSignature):
		return string(CodeInvalidSignature)
	case errors.Is(err, ErrInvalidPublicKey):
		return string(CodeInvalidPublicKey)
	case errors.Is(err, ErrWrongProduct):
		return string(CodeWrongProduct)
	case errors.Is(err, ErrWrongChannel):
		return string(CodeWrongChannel)
	case errors.Is(err, ErrWrongMachine):
		return string(CodeWrongMachine)
	case errors.Is(err, ErrExpired):
		return string(CodeExpired)
	default:
		return GateErrorVerification
	}
}
