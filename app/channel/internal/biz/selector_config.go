package biz

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// SelectorFailureThresholdFromEnv validates the independent consecutive-failure gate.
func SelectorFailureThresholdFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv("CHANNEL_SELECTOR_CONSECUTIVE_FAILURES"))
	if raw == "" {
		return 5, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("CHANNEL_SELECTOR_CONSECUTIVE_FAILURES must be a positive integer")
	}
	return n, nil
}
func selectorFailureThreshold() int {
	n, err := SelectorFailureThresholdFromEnv()
	if err != nil {
		panic(err)
	} // production config loader validates before wiring
	return n
}
