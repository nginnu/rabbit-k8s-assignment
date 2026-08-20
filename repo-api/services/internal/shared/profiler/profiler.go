package profiler

import (
	"log/slog"
	"os"
	"runtime"

	"github.com/grafana/pyroscope-go"
)

// Init starts Pyroscope continuous profiler.
// Sends profiles to PYROSCOPE_URL (default: http://otel-collector:4040).
func Init(serviceName string) {
	url := os.Getenv("PYROSCOPE_URL")
	if url == "" {
		url = "http://otel-collector:4040"
	}

	runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(5)

	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: serviceName,
		ServerAddress:   url,
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})
	if err != nil {
		slog.Warn("pyroscope init failed", "err", err)
		return
	}
	slog.Info("pyroscope started", "service", serviceName, "url", url)
}
