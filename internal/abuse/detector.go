package abuse

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// Detector runs the ladder on a schedule: collect signals for every active
// sender, evaluate each against the thresholds, apply the resulting rung.
//
// It is deliberately a poller rather than an inline check on the send path. The
// signals it reads are aggregates that only move over minutes-to-hours, and
// making every send pay for two aggregate queries would trade a real latency
// cost for detection speed the signals cannot actually deliver.
type Detector struct {
	signals SignalSource
	ladder  *Ladder
	cfg     Config
	logger  *zap.Logger
}

// NewDetector builds the scanner.
func NewDetector(signals SignalSource, ladder *Ladder, cfg Config, logger *zap.Logger) *Detector {
	return &Detector{signals: signals, ladder: ladder, cfg: cfg, logger: logger}
}

// ScanResult summarises one pass, for logging and tests.
type ScanResult struct {
	// Evaluated is how many active senders were judged.
	Evaluated int

	// Actioned counts accounts whose applied rung was above RungNone.
	Actioned int

	// ByRung breaks the actioned accounts down by applied rung.
	ByRung map[Rung]int
}

// Scan performs a single evaluation pass over all active senders.
//
// A failure on one account does not abort the pass: one unwritable mark must not
// leave every other abusive account unenforced.
func (d *Detector) Scan(ctx context.Context) (ScanResult, error) {
	result := ScanResult{ByRung: make(map[Rung]int)}

	signals, err := d.signals.Collect(ctx, d.cfg.Thresholds.Window)
	if err != nil {
		return result, err
	}

	for _, s := range signals {
		if s.Pubkey == "" {
			continue
		}
		result.Evaluated++

		warranted, reason := d.cfg.Thresholds.Evaluate(s)

		applied, err := d.ladder.Apply(ctx, s.Pubkey, warranted, reason)
		if err != nil {
			d.logger.Error("Failed to apply abuse rung",
				zap.String("pubkey", s.Pubkey),
				zap.String("warranted", warranted.String()),
				zap.Error(err))
			continue
		}

		if applied > RungNone {
			result.Actioned++
			result.ByRung[applied]++
		}
	}

	return result, nil
}

// Run scans every interval until ctx is cancelled. Intended to be started in a
// goroutine at boot.
func (d *Detector) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	d.logger.Info("Abuse detection ladder started",
		zap.Duration("interval", interval),
		zap.Duration("window", d.cfg.Thresholds.Window),
		zap.Bool("auto_suspend", d.cfg.Thresholds.AllowAutoSuspend))

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Abuse detection ladder stopped")
			return
		case <-ticker.C:
			result, err := d.Scan(ctx)
			if err != nil {
				d.logger.Error("Abuse detection scan failed", zap.Error(err))
				continue
			}
			if result.Actioned > 0 {
				d.logger.Warn("Abuse detection scan actioned accounts",
					zap.Int("evaluated", result.Evaluated),
					zap.Int("actioned", result.Actioned),
					zap.Int("throttled", result.ByRung[RungThrottle]),
					zap.Int("held", result.ByRung[RungHold]),
					zap.Int("suspended", result.ByRung[RungSuspend]))
			} else {
				d.logger.Debug("Abuse detection scan clean",
					zap.Int("evaluated", result.Evaluated))
			}
		}
	}
}
