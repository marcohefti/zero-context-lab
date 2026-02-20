package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/config"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/suite"
)

const (
	attemptStatusAny             = "any"
	attemptStatusOK              = "ok"
	attemptStatusFail            = "fail"
	attemptStatusMissingFeedback = "missing_feedback"
)

type attemptIndexFilter struct {
	SuiteID string
	Mission string
	Status  string
	Tags    []string
	Limit   int
	OutRoot string
}

type attemptIndexRow struct {
	RunID           string                   `json:"runId"`
	SuiteID         string                   `json:"suiteId"`
	MissionID       string                   `json:"missionId"`
	AttemptID       string                   `json:"attemptId"`
	Mode            string                   `json:"mode,omitempty"`
	Status          string                   `json:"status"`
	OK              *bool                    `json:"ok,omitempty"`
	StartedAt       string                   `json:"startedAt,omitempty"`
	EndedAt         string                   `json:"endedAt,omitempty"`
	Classification  string                   `json:"classification,omitempty"`
	DecisionTags    []string                 `json:"decisionTags,omitempty"`
	Tags            []string                 `json:"tags,omitempty"`
	FeedbackPresent bool                     `json:"feedbackPresent"`
	TraceNonEmpty   bool                     `json:"traceNonEmpty"`
	TokenEstimates  *schema.TokenEstimatesV1 `json:"tokenEstimates,omitempty"`
	AttemptDir      string                   `json:"attemptDir"`
}

type runIndexRow struct {
	RunID                  string `json:"runId"`
	SuiteID                string `json:"suiteId"`
	CreatedAt              string `json:"createdAt"`
	Status                 string `json:"status"`
	AttemptsTotal          int    `json:"attemptsTotal"`
	OKTotal                int    `json:"okTotal"`
	FailTotal              int    `json:"failTotal"`
	MissingFeedbackTotal   int    `json:"missingFeedbackTotal"`
	LatestAttemptID        string `json:"latestAttemptId,omitempty"`
	LatestAttemptMission   string `json:"latestAttemptMission,omitempty"`
	LatestAttemptStartedAt string `json:"latestAttemptStartedAt,omitempty"`
	RunDir                 string `json:"runDir"`
}

func (r Runner) runAttemptList(args []string) int {
	fs := flag.NewFlagSet("attempt list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	suiteID := fs.String("suite", "", "filter by suiteId")
	missionID := fs.String("mission", "", "filter by missionId")
	status := fs.String("status", attemptStatusAny, "filter by status: any|ok|fail|missing_feedback")
	limit := fs.Int("limit", 0, "max rows (0 = all)")
	var tags stringListFlag
	fs.Var(&tags, "tag", "filter by mission tag (repeatable)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("attempt list: invalid flags")
	}
	if *help {
		printAttemptHelp(r.Stdout)
		return 0
	}
	if !*jsonOut {
		printAttemptHelp(r.Stderr)
		return r.failUsage("attempt list: require --json for stable output")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	filter := attemptIndexFilter{
		SuiteID: strings.TrimSpace(*suiteID),
		Mission: strings.TrimSpace(*missionID),
		Status:  normalizeAttemptStatus(*status),
		Tags:    dedupeSortedStrings([]string(tags)),
		Limit:   *limit,
		OutRoot: m.OutRoot,
	}
	if !isValidAttemptStatus(filter.Status) {
		return r.failUsage("attempt list: invalid --status (expected any|ok|fail|missing_feedback)")
	}
	if filter.Limit < 0 {
		return r.failUsage("attempt list: --limit must be >= 0")
	}

	rows, err := collectAttemptRows(filter)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	total := len(rows)
	if filter.Limit > 0 && len(rows) > filter.Limit {
		rows = rows[:filter.Limit]
	}

	return r.writeJSON(struct {
		OK       bool              `json:"ok"`
		OutRoot  string            `json:"outRoot"`
		Total    int               `json:"total"`
		Returned int               `json:"returned"`
		Attempts []attemptIndexRow `json:"attempts"`
	}{
		OK:       true,
		OutRoot:  m.OutRoot,
		Total:    total,
		Returned: len(rows),
		Attempts: rows,
	})
}

func (r Runner) runAttemptLatest(args []string) int {
	fs := flag.NewFlagSet("attempt latest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	suiteID := fs.String("suite", "", "filter by suiteId")
	missionID := fs.String("mission", "", "filter by missionId")
	status := fs.String("status", attemptStatusAny, "filter by status: any|ok|fail|missing_feedback")
	var tags stringListFlag
	fs.Var(&tags, "tag", "filter by mission tag (repeatable)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("attempt latest: invalid flags")
	}
	if *help {
		printAttemptHelp(r.Stdout)
		return 0
	}
	if !*jsonOut {
		printAttemptHelp(r.Stderr)
		return r.failUsage("attempt latest: require --json for stable output")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	filter := attemptIndexFilter{
		SuiteID: strings.TrimSpace(*suiteID),
		Mission: strings.TrimSpace(*missionID),
		Status:  normalizeAttemptStatus(*status),
		Tags:    dedupeSortedStrings([]string(tags)),
		Limit:   1,
		OutRoot: m.OutRoot,
	}
	if !isValidAttemptStatus(filter.Status) {
		return r.failUsage("attempt latest: invalid --status (expected any|ok|fail|missing_feedback)")
	}

	rows, err := collectAttemptRows(filter)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if len(rows) == 0 {
		return r.writeJSON(struct {
			OK      bool   `json:"ok"`
			OutRoot string `json:"outRoot"`
			Found   bool   `json:"found"`
		}{OK: true, OutRoot: m.OutRoot, Found: false})
	}
	return r.writeJSON(struct {
		OK      bool            `json:"ok"`
		OutRoot string          `json:"outRoot"`
		Found   bool            `json:"found"`
		Attempt attemptIndexRow `json:"attempt"`
	}{OK: true, OutRoot: m.OutRoot, Found: true, Attempt: rows[0]})
}

func (r Runner) runRunsList(args []string) int {
	fs := flag.NewFlagSet("runs list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	suiteID := fs.String("suite", "", "filter by suiteId")
	status := fs.String("status", attemptStatusAny, "filter by run status: any|ok|fail|missing_feedback")
	limit := fs.Int("limit", 0, "max rows (0 = all)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("runs list: invalid flags")
	}
	if *help {
		printRunsHelp(r.Stdout)
		return 0
	}
	if !*jsonOut {
		printRunsHelp(r.Stderr)
		return r.failUsage("runs list: require --json for stable output")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	statusFilter := normalizeAttemptStatus(*status)
	if !isValidAttemptStatus(statusFilter) {
		return r.failUsage("runs list: invalid --status (expected any|ok|fail|missing_feedback)")
	}
	if *limit < 0 {
		return r.failUsage("runs list: --limit must be >= 0")
	}

	rows, err := collectRunRows(m.OutRoot, strings.TrimSpace(*suiteID))
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if statusFilter != attemptStatusAny {
		filtered := make([]runIndexRow, 0, len(rows))
		for _, row := range rows {
			if row.Status == statusFilter {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	total := len(rows)
	if *limit > 0 && len(rows) > *limit {
		rows = rows[:*limit]
	}

	return r.writeJSON(struct {
		OK       bool          `json:"ok"`
		OutRoot  string        `json:"outRoot"`
		Total    int           `json:"total"`
		Returned int           `json:"returned"`
		Runs     []runIndexRow `json:"runs"`
	}{
		OK:       true,
		OutRoot:  m.OutRoot,
		Total:    total,
		Returned: len(rows),
		Runs:     rows,
	})
}

func collectRunRows(outRoot string, suiteFilter string) ([]runIndexRow, error) {
	absOutRoot, err := filepath.Abs(outRoot)
	if err != nil {
		return nil, err
	}
	runsDir := filepath.Join(absOutRoot, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var rows []runIndexRow
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runDir := filepath.Join(runsDir, e.Name())
		var runMeta schema.RunJSONV1
		if !readJSONIfExists(filepath.Join(runDir, "run.json"), &runMeta) {
			continue
		}
		if suiteFilter != "" && runMeta.SuiteID != suiteFilter {
			continue
		}

		row := runIndexRow{
			RunID:     runMeta.RunID,
			SuiteID:   runMeta.SuiteID,
			CreatedAt: runMeta.CreatedAt,
			RunDir:    runDir,
		}
		attempts, err := collectAttemptRows(attemptIndexFilter{
			SuiteID: runMeta.SuiteID,
			Status:  attemptStatusAny,
			OutRoot: absOutRoot,
		})
		if err != nil {
			return nil, err
		}
		for _, a := range attempts {
			if a.RunID != row.RunID {
				continue
			}
			row.AttemptsTotal++
			switch a.Status {
			case attemptStatusOK:
				row.OKTotal++
			case attemptStatusFail:
				row.FailTotal++
			default:
				row.MissingFeedbackTotal++
			}
			if isMoreRecentTS(a.StartedAt, row.LatestAttemptStartedAt) {
				row.LatestAttemptID = a.AttemptID
				row.LatestAttemptMission = a.MissionID
				row.LatestAttemptStartedAt = a.StartedAt
			}
		}
		row.Status = runStatus(row)
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		ti, _ := parseTS(rows[i].CreatedAt)
		tj, _ := parseTS(rows[j].CreatedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return rows[i].RunID > rows[j].RunID
	})
	return rows, nil
}

func collectAttemptRows(filter attemptIndexFilter) ([]attemptIndexRow, error) {
	absOutRoot, err := filepath.Abs(filter.OutRoot)
	if err != nil {
		return nil, err
	}
	runsDir := filepath.Join(absOutRoot, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	rows := make([]attemptIndexRow, 0, 128)
	for _, runEntry := range entries {
		if !runEntry.IsDir() {
			continue
		}
		runDir := filepath.Join(runsDir, runEntry.Name())
		var runMeta schema.RunJSONV1
		if !readJSONIfExists(filepath.Join(runDir, "run.json"), &runMeta) {
			continue
		}
		if filter.SuiteID != "" && runMeta.SuiteID != filter.SuiteID {
			continue
		}

		tagsByMission := loadMissionTags(filepath.Join(runDir, "suite.json"))
		attemptsDir := filepath.Join(runDir, "attempts")
		attemptEntries, err := os.ReadDir(attemptsDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, aEntry := range attemptEntries {
			if !aEntry.IsDir() {
				continue
			}
			attemptDir := filepath.Join(attemptsDir, aEntry.Name())
			var a schema.AttemptJSONV1
			if !readJSONIfExists(filepath.Join(attemptDir, "attempt.json"), &a) {
				continue
			}
			if filter.Mission != "" && a.MissionID != filter.Mission {
				continue
			}

			row := attemptIndexRow{
				RunID:      a.RunID,
				SuiteID:    a.SuiteID,
				MissionID:  a.MissionID,
				AttemptID:  a.AttemptID,
				Mode:       a.Mode,
				Status:     attemptStatusMissingFeedback,
				StartedAt:  a.StartedAt,
				Tags:       append([]string(nil), tagsByMission[a.MissionID]...),
				AttemptDir: attemptDir,
			}
			if len(filter.Tags) > 0 && !hasTagOverlap(row.Tags, filter.Tags) {
				continue
			}

			var fb schema.FeedbackJSONV1
			if readJSONIfExists(filepath.Join(attemptDir, "feedback.json"), &fb) {
				row.FeedbackPresent = true
				ok := fb.OK
				row.OK = &ok
				row.Status = attemptStatusFail
				if fb.OK {
					row.Status = attemptStatusOK
				}
				row.EndedAt = fb.CreatedAt
				row.Classification = fb.Classification
				row.DecisionTags = append([]string(nil), fb.DecisionTags...)
			}

			var rep schema.AttemptReportJSONV1
			if readJSONIfExists(filepath.Join(attemptDir, "attempt.report.json"), &rep) {
				if row.EndedAt == "" {
					row.EndedAt = rep.EndedAt
				}
				if rep.TokenEstimates != nil {
					row.TokenEstimates = rep.TokenEstimates
				}
				if rep.Integrity != nil {
					row.TraceNonEmpty = rep.Integrity.TraceNonEmpty
					if !row.FeedbackPresent {
						row.FeedbackPresent = rep.Integrity.FeedbackPresent
					}
				}
			}
			if !row.TraceNonEmpty {
				nonEmpty, err := store.JSONLHasNonEmptyLine(filepath.Join(attemptDir, "tool.calls.jsonl"))
				if err == nil {
					row.TraceNonEmpty = nonEmpty
				}
			}

			if filter.Status != attemptStatusAny && row.Status != filter.Status {
				continue
			}
			rows = append(rows, row)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		ti, _ := parseTS(rows[i].StartedAt)
		tj, _ := parseTS(rows[j].StartedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		if rows[i].RunID != rows[j].RunID {
			return rows[i].RunID > rows[j].RunID
		}
		return rows[i].AttemptID > rows[j].AttemptID
	})
	return rows, nil
}

func parseTS(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err == nil {
		return ts, true
	}
	ts, err = time.Parse(time.RFC3339, s)
	if err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func isMoreRecentTS(a, b string) bool {
	ta, oka := parseTS(a)
	tb, okb := parseTS(b)
	switch {
	case oka && okb:
		return ta.After(tb)
	case oka && !okb:
		return true
	case !oka && okb:
		return false
	default:
		return strings.TrimSpace(a) > strings.TrimSpace(b)
	}
}

func runStatus(row runIndexRow) string {
	if row.MissingFeedbackTotal > 0 {
		return attemptStatusMissingFeedback
	}
	if row.FailTotal > 0 {
		return attemptStatusFail
	}
	if row.OKTotal > 0 {
		return attemptStatusOK
	}
	return attemptStatusMissingFeedback
}

func normalizeAttemptStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return attemptStatusAny
	}
	return s
}

func isValidAttemptStatus(s string) bool {
	switch s {
	case attemptStatusAny, attemptStatusOK, attemptStatusFail, attemptStatusMissingFeedback:
		return true
	default:
		return false
	}
}

func hasTagOverlap(tags []string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	if len(tags) == 0 {
		return false
	}
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
		seen[strings.TrimSpace(t)] = true
	}
	for _, t := range filter {
		if seen[strings.TrimSpace(t)] {
			return true
		}
	}
	return false
}

func loadMissionTags(path string) map[string][]string {
	out := map[string][]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var sf suite.SuiteFileV1
	if err := json.Unmarshal(raw, &sf); err != nil {
		return out
	}
	for _, m := range sf.Missions {
		if len(m.Tags) == 0 {
			continue
		}
		cp := append([]string(nil), m.Tags...)
		sort.Strings(cp)
		out[m.MissionID] = cp
	}
	return out
}

func readJSONIfExists(path string, out any) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false
	}
	return true
}
