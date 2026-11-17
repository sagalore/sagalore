package contract

import (
	"strconv"
	"strings"
)

// ProtocolHeader is sent by the companion on every API call.
// It is independent of the companion's release version.
const ProtocolHeader = "X-Sagalore-Protocol"

// ProtocolVersion is the wire protocol this build speaks.
// MinProtocolVersion is the oldest companion the API still accepts
// (current and previous). A missing header is protocol 1, the
// pre-handshake companion.
//
// The addon copy of this number is SagaloreObjectives.CODE_PROTOCOL.
const (
	ProtocolVersion    = 2
	MinProtocolVersion = 1
)

// ProtocolInfo is advertised on GET /v1/sync and on an upgrade response.
type ProtocolInfo struct {
	Version int `json:"version"`
	Min     int `json:"min"`
	Max     int `json:"max"`
}

// CurrentProtocol is what this server speaks and accepts.
func CurrentProtocol() ProtocolInfo {
	return ProtocolInfo{Version: ProtocolVersion, Min: MinProtocolVersion, Max: ProtocolVersion}
}

// ProtocolCheck is the result of reading a companion protocol header.
type ProtocolCheck struct {
	Client  int
	OK      bool
	TooOld  bool
	TooNew  bool
	Invalid bool
}

// CheckProtocol reads X-Sagalore-Protocol.
// An empty header is protocol 1 so companions from before the handshake
// stay on the supported previous version. A non-integer is invalid.
func CheckProtocol(header string) ProtocolCheck {
	header = strings.TrimSpace(header)
	client := 1
	if header != "" {
		n, err := strconv.Atoi(header)
		if err != nil || n < 0 {
			return ProtocolCheck{Invalid: true}
		}
		client = n
	}
	info := CurrentProtocol()
	if client < info.Min {
		return ProtocolCheck{Client: client, TooOld: true}
	}
	if client > info.Max {
		return ProtocolCheck{Client: client, TooNew: true}
	}
	return ProtocolCheck{Client: client, OK: true}
}
