package miot

// ErrorCode identifies a structured MIoT error category.
type ErrorCode string

const (
	// ErrInvalidArgument reports invalid caller input.
	ErrInvalidArgument ErrorCode = "invalid_argument"
	// ErrUnauthorized reports a generic authorization failure.
	ErrUnauthorized ErrorCode = "unauthorized"
	// ErrInvalidAccessToken reports that the current access token is rejected.
	ErrInvalidAccessToken ErrorCode = "invalid_access_token"
	// ErrInvalidResponse reports an unexpected or malformed response payload.
	ErrInvalidResponse ErrorCode = "invalid_response"
	// ErrDecodeResponse reports that a response body could not be decoded.
	ErrDecodeResponse ErrorCode = "decode_response"
	// ErrTransportFailure reports network or transport-layer failures.
	ErrTransportFailure ErrorCode = "transport_failure"
	// ErrStorageFailure reports file or persistence failures.
	ErrStorageFailure ErrorCode = "storage_failure"
	// ErrCertificateInvalid reports invalid certificate material.
	ErrCertificateInvalid ErrorCode = "certificate_invalid"
	// ErrSpecNotFound reports that a requested MIoT spec could not be found.
	ErrSpecNotFound ErrorCode = "spec_not_found"
	// ErrProtocolFailure reports protocol-level failures.
	ErrProtocolFailure ErrorCode = "protocol_failure"
	// ErrFeatureNotPortable reports functionality that cannot be ported directly.
	ErrFeatureNotPortable ErrorCode = "feature_not_portable"
)
