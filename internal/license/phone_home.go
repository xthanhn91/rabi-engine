package license

import (
	"context"
	"log/slog"
	"time"
)

// PhoneHome runs a background goroutine that validates the license
// every PhoneHomeInterval (24h). If validation fails, it logs a
// warning but does NOT stop the running server (grace period applies
// only at startup).
//
// Returns a cancel function to stop the goroutine.
func PhoneHome(ctx context.Context, validator *Validator, key string) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(PhoneHomeInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Debug("license phone-home stopped")
				return
			case <-ticker.C:
				resp, err := validator.Validate(key)
				if err != nil {
					slog.Warn("license phone-home failed", "error", err)
					continue
				}
				slog.Info("license phone-home success",
					"plan", resp.Plan,
					"max_agents", resp.MaxAgents,
				)
			}
		}
	}()

	return cancel
}
