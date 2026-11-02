package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// UsageAccumulator accepts normalized provider reports. Unknown usage stays
// unknown; output length and prompt size are never presented as billed tokens.
type UsageAccumulator struct {
	mu            sync.Mutex
	input, output int64
	calls         int
	model         string
	seen          map[string]bool
}

func (u *UsageAccumulator) Observe(e StreamEvent) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if e.Model != "" {
		u.model = e.Model
	}
	if e.Type != "usage" || e.Usage == nil || e.Usage.PromptTokens < 0 || e.Usage.CompletionTokens < 0 {
		return
	}
	if e.ID != "" {
		if u.seen == nil {
			u.seen = make(map[string]bool)
		}
		if u.seen[e.ID] {
			return
		}
		u.seen[e.ID] = true
	}
	u.input += int64(e.Usage.PromptTokens)
	u.output += int64(e.Usage.CompletionTokens)
	u.calls++
}

func (u *UsageAccumulator) Snapshot() (input, output int64, calls int, model string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.input, u.output, u.calls, u.model
}

type UsageRecorder struct {
	UsageAccumulator
	core    *Core
	id      string
	started time.Time
}

// BeginUsage records one user turn or worker invocation in the profile's
// encrypted store. It also supports hosts constructed without persistence.
func (c *Core) BeginUsage(ctx context.Context, cli, model, surface string) (*UsageRecorder, error) {
	u := &UsageRecorder{core: c, started: time.Now().UTC()}
	u.model = model
	if c.store == nil {
		return u, nil
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	u.id = hex.EncodeToString(id[:])
	_, err := c.store.DB().ExecContext(ctx, `INSERT INTO usage_runs(id, started_at, cli, model, surface) VALUES(?,?,?,?,?)`, u.id, u.started.Format(time.RFC3339Nano), cli, model, surface)
	return u, err
}

func (u *UsageRecorder) Finish(runErr error) error {
	if u == nil || u.core.store == nil {
		return nil
	}
	input, output, calls, model := u.Snapshot()
	outcome := "completed"
	if errors.Is(runErr, context.Canceled) {
		outcome = "cancelled"
	} else if runErr != nil {
		outcome = "failed"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := u.core.store.DB().ExecContext(ctx, `UPDATE usage_runs SET model=?, outcome=?, duration_ms=?, input_tokens=?, output_tokens=?, reported_calls=? WHERE id=?`, model, outcome, time.Since(u.started).Milliseconds(), input, output, calls, u.id)
	if err != nil {
		return fmt.Errorf("save usage metrics: %w", err)
	}
	return nil
}

type UsageTotals struct {
	Runs                int     `json:"runs"`
	ReportedRuns        int     `json:"reportedRuns"`
	InputTokens         int64   `json:"inputTokens"`
	OutputTokens        int64   `json:"outputTokens"`
	Tokens              int64   `json:"tokens"`
	ActiveDays          int     `json:"activeDays"`
	AverageTokensPerRun float64 `json:"averageTokensPerRun"`
	AverageTokensPerDay float64 `json:"averageTokensPerDay"`
}
type UsageBucket struct {
	Name         string `json:"name"`
	Runs         int    `json:"runs"`
	ReportedRuns int    `json:"reportedRuns"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	Tokens       int64  `json:"tokens"`
}
type UsageDashboard struct {
	Month         string        `json:"month"`
	TrackingSince string        `json:"trackingSince"`
	Totals        UsageTotals   `json:"totals"`
	CLIs          []UsageBucket `json:"clis"`
	Models        []UsageBucket `json:"models"`
	Days          []UsageBucket `json:"days"`
	Months        []UsageBucket `json:"months"`
	Surfaces      []UsageBucket `json:"surfaces"`
}

func (c *Core) UsageDashboard(ctx context.Context, month string) (*UsageDashboard, error) {
	if c.store == nil {
		return nil, errors.New("usage dashboard requires an unlocked database")
	}
	now := time.Now().UTC()
	if month == "" {
		month = now.Format("2006-01")
	}
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, errors.New("month must use YYYY-MM")
	}
	end := start.AddDate(0, 1, 0)
	d := &UsageDashboard{Month: month, CLIs: []UsageBucket{}, Models: []UsageBucket{}, Days: []UsageBucket{}, Months: []UsageBucket{}, Surfaces: []UsageBucket{}}
	db := c.store.DB()
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MIN(started_at),'') FROM usage_runs`).Scan(&d.TrackingSince); err != nil {
		return nil, err
	}
	from, to := start.Format("2006-01-02"), end.Format("2006-01-02")
	const counts = `COUNT(*), COALESCE(SUM(reported_calls>0),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)`
	if err := db.QueryRowContext(ctx, `SELECT `+counts+`, COUNT(DISTINCT substr(started_at,1,10)) FROM usage_runs WHERE started_at>=? AND started_at<?`, from, to).Scan(&d.Totals.Runs, &d.Totals.ReportedRuns, &d.Totals.InputTokens, &d.Totals.OutputTokens, &d.Totals.ActiveDays); err != nil {
		return nil, err
	}
	d.Totals.Tokens = d.Totals.InputTokens + d.Totals.OutputTokens
	if d.Totals.ReportedRuns > 0 {
		d.Totals.AverageTokensPerRun = float64(d.Totals.Tokens) / float64(d.Totals.ReportedRuns)
	}
	days := end.Sub(start).Hours() / 24
	if !now.Before(start) && now.Before(end) {
		days = max(1, float64(now.Day()))
	}
	d.Totals.AverageTokensPerDay = float64(d.Totals.Tokens) / days
	group := func(expression, lo, hi, order string) ([]UsageBucket, error) {
		rows, err := db.QueryContext(ctx, `SELECT `+expression+`, `+counts+` FROM usage_runs WHERE started_at>=? AND started_at<? GROUP BY 1 ORDER BY `+order, lo, hi)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := make([]UsageBucket, 0, 8)
		for rows.Next() {
			var b UsageBucket
			if err := rows.Scan(&b.Name, &b.Runs, &b.ReportedRuns, &b.InputTokens, &b.OutputTokens); err != nil {
				return nil, err
			}
			b.Tokens = b.InputTokens + b.OutputTokens
			result = append(result, b)
		}
		return result, rows.Err()
	}
	if d.Totals.Runs > 0 {
		if d.CLIs, err = group("cli", from, to, "2 DESC, 1"); err != nil {
			return nil, err
		}
		if d.Models, err = group("CASE WHEN model='' THEN 'CLI default (unreported)' ELSE model END", from, to, "2 DESC, 1"); err != nil {
			return nil, err
		}
		if d.Surfaces, err = group("surface", from, to, "2 DESC, 1"); err != nil {
			return nil, err
		}
		if d.Days, err = group("substr(started_at,1,10)", from, to, "1"); err != nil {
			return nil, err
		}
	}
	if d.Months, err = group("substr(started_at,1,7)", start.AddDate(0, -5, 0).Format("2006-01-02"), to, "1"); err != nil {
		return nil, err
	}
	return d, nil
}
