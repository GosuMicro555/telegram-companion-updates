package updater

import "errors"

// ErrorCode is a bounded, non-secret classification of an update failure.
// Values in this type are safe to cross the Wails boundary; native error
// descriptions, paths, and nested NSError data are deliberately excluded.
type ErrorCode string

const (
	CodeSignatureInvalid     ErrorCode = "update_signature_invalid"
	CodeValidationFailed     ErrorCode = "update_validation_failed"
	CodeRunningFromDiskImage ErrorCode = "update_running_from_disk_image"
	CodeInstallFailed        ErrorCode = "update_install_failed"
)

type codedError struct {
	code  ErrorCode
	cause error
}

// NewCodedError wraps an internal cause with one of the stable update
// classifications. Unknown codes are rejected and retain only the cause (or a
// generic error when no cause is supplied), so callers cannot smuggle arbitrary
// strings into a user-facing status.
func NewCodedError(code ErrorCode, cause error) error {
	if !IsKnownErrorCode(code) {
		if cause != nil {
			return cause
		}
		return errors.New("update operation failed")
	}
	if cause == nil {
		cause = errors.New(safeErrorText(code))
	}
	return codedError{code: code, cause: cause}
}

func (e codedError) Error() string {
	return safeErrorText(e.code)
}

func (e codedError) Unwrap() error { return e.cause }

func (e codedError) UpdateErrorCode() ErrorCode { return e.code }

// ErrorCodeOf extracts a known code from an error chain. Unknown or untyped
// errors return an empty code and are handled by the generic update failure
// path.
func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var coded interface{ UpdateErrorCode() ErrorCode }
	if !errors.As(err, &coded) {
		return ""
	}
	code := coded.UpdateErrorCode()
	if !IsKnownErrorCode(code) {
		return ""
	}
	return code
}

// IsKnownErrorCode reports whether code is part of the intentionally small
// diagnostic allowlist.
func IsKnownErrorCode(code ErrorCode) bool {
	switch code {
	case CodeSignatureInvalid, CodeValidationFailed, CodeRunningFromDiskImage, CodeInstallFailed:
		return true
	default:
		return false
	}
}

func safeErrorText(code ErrorCode) string {
	switch code {
	case CodeSignatureInvalid:
		return "update signature verification failed"
	case CodeValidationFailed:
		return "update validation failed"
	case CodeRunningFromDiskImage:
		return "the application must be copied out of the disk image before updating"
	case CodeInstallFailed:
		return "update installation failed"
	default:
		return "update operation failed"
	}
}
