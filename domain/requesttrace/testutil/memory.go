// Memory probes run only inside dedicated Linux cgroups, never in ordinary unit tests.
package testutil

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func MemoryPoint(t testing.TB) map[string]uint64 {
	t.Helper()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	point := map[string]uint64{"heap_alloc": mem.HeapAlloc}
	status, err := os.ReadFile("/proc/self/status")
	require.NoError(t, err)
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "VmRSS:" || fields[0] == "VmHWM:") {
			value, err := strconv.ParseUint(fields[1], 10, 64)
			require.NoError(t, err)
			point[fields[0]] = value * 1024
		}
	}
	for _, metric := range []string{"memory.current", "memory.peak", "memory.max"} {
		encoded, err := os.ReadFile("/sys/fs/cgroup/" + metric)
		require.NoError(t, err)
		value, err := strconv.ParseUint(strings.TrimSpace(string(encoded)), 10, 64)
		require.NoError(t, err)
		point[metric] = value
	}
	return point
}

func CheckMemoryBudget(t testing.TB, budget uint64) {
	t.Helper()
	if os.Getenv("TIMING_MAXIMUM_MEMORY") != "1" {
		return
	}
	point := MemoryPoint(t)
	report, err := jsonx.MarshalIndent(map[string]any{"budget_bytes": budget, "point": point}, "", "  ")
	require.NoError(t, err)
	t.Log(string(report))
	if path := os.Getenv("TIMING_MEMORY_OUTPUT"); path != "" {
		require.NoError(t, os.WriteFile(path, report, 0600))
	}
	require.LessOrEqual(t, point["VmHWM:"], budget)
	require.LessOrEqual(t, point["memory.peak"], budget)
}
