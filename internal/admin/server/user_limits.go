package server

import (
	"net/http"
	"os"
	"strconv"
	"strings"
)

const defaultUserRPMLimit = 3

func handleUserLimits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse(false, "method not allowed", nil))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{
		"user_rpm_limit": userRPMLimitFromEnv(),
	}))
}

func userRPMLimitFromEnv() int32 {
	raw := strings.TrimSpace(os.Getenv("SUBSCRIPTION_USER_RPM_LIMIT"))
	if raw == "" {
		return defaultUserRPMLimit
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 {
		return defaultUserRPMLimit
	}
	return int32(value)
}
