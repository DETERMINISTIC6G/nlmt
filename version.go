package nlmt

// Version is the IRTTF version number (replaced during build).
var Version = "0.10"

// ProtocolVersion is the protocol version number, which must match between client
// and server.
var ProtocolVersion = 1

// JSONFormatVersion is the JSON format number.
var JSONFormatVersion = 1

// VersionInfo stores the version information.
type VersionInfo struct {
	IRTTF      string `json:"irttf"`
	Protocol   int    `json:"protocol"`
	JSONFormat int    `json:"json_format"`
}

// NewVersionInfo returns a new VersionInfo.
func NewVersionInfo() *VersionInfo {
	return &VersionInfo{
		IRTTF:      Version,
		Protocol:   ProtocolVersion,
		JSONFormat: JSONFormatVersion,
	}
}
