package sparkle

const (
	nativeStateIdle = iota
	nativeStatePending
	nativeStateDone
	nativeStateFailed
)

const (
	nativeOperationCheck = iota + 1
	nativeOperationDownload
	nativeOperationInstall
)
