package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/sagalore/sagalore/internal/contract"
)

// protocolGate rejects companions outside the supported protocol range
// before the route runs, so a mismatch is a JSON error instead of a
// half-applied request. Auth and public pages stay open: an old companion
// can still sign in, then learn it must update on the next /v1 call.
func protocolGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !protocolPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		check := contract.CheckProtocol(r.Header.Get(contract.ProtocolHeader))
		if check.OK {
			next.ServeHTTP(w, r)
			return
		}
		info := contract.CurrentProtocol()
		code := "upgrade_required"
		status := http.StatusUpgradeRequired
		var msg string
		switch {
		case check.Invalid:
			code = "invalid_protocol"
			status = http.StatusBadRequest
			msg = fmt.Sprintf("%s must be a non-negative integer. This server accepts protocol %d through %d.", contract.ProtocolHeader, info.Min, info.Max)
		case check.TooOld:
			msg = fmt.Sprintf("Update Sagalore. This companion speaks protocol %d; the server requires %d–%d. Install the update, then type /reload in game.", check.Client, info.Min, info.Max)
		default:
			code = "protocol_mismatch"
			msg = fmt.Sprintf("This companion speaks protocol %d, but the server only supports %d–%d. Update the server.", check.Client, info.Min, info.Max)
		}
		writeJSON(w, status, map[string]any{
			"error":    code,
			"message":  msg,
			"protocol": info,
			"client":   check.Client,
		})
	})
}

func protocolPath(path string) bool {
	if !strings.HasPrefix(path, "/v1/") {
		return false
	}
	switch {
	case strings.HasPrefix(path, "/v1/auth/"):
		return false
	case strings.HasPrefix(path, "/v1/public/"):
		return false
	case strings.HasPrefix(path, "/v1/join/"):
		return false
	default:
		return true
	}
}
