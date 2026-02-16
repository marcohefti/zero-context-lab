package planner

import (
	"time"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	"github.com/marcohefti/zero-context-lab/internal/suite"
)

type SuitePlanOpts struct {
	OutRoot   string
	RunID     string
	SuiteFile string
	Mode      string
	TimeoutMs int64
}

type PlannedMission struct {
	MissionID string            `json:"missionId"`
	Prompt    string            `json:"prompt,omitempty"`
	AttemptID string            `json:"attemptId"`
	OutDir    string            `json:"outDir"`
	OutDirAbs string            `json:"outDirAbs"`
	Env       map[string]string `json:"env"`
}

type SuitePlanResult struct {
	OK        bool             `json:"ok"`
	RunID     string           `json:"runId"`
	SuiteID   string           `json:"suiteId"`
	Mode      string           `json:"mode"`
	OutRoot   string           `json:"outRoot"`
	Missions  []PlannedMission `json:"missions"`
	CreatedAt string           `json:"createdAt"`
}

func PlanSuite(now time.Time, opts SuitePlanOpts) (SuitePlanResult, error) {
	parsed, err := suite.ParseFile(opts.SuiteFile)
	if err != nil {
		return SuitePlanResult{}, err
	}

	mode := opts.Mode
	if mode == "" {
		mode = parsed.Suite.Defaults.Mode
	}

	timeoutMs := opts.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = parsed.Suite.Defaults.TimeoutMs
	}

	rid := opts.RunID
	var missions []PlannedMission
	for _, sm := range parsed.Suite.Missions {
		ar, err := attempt.Start(now, attempt.StartOpts{
			OutRoot:       opts.OutRoot,
			RunID:         rid,
			SuiteID:       parsed.Suite.SuiteID,
			MissionID:     sm.MissionID,
			Mode:          mode,
			Retry:         1,
			Prompt:        sm.Prompt,
			TimeoutMs:     timeoutMs,
			SuiteSnapshot: parsed.CanonicalJSON,
		})
		if err != nil {
			return SuitePlanResult{}, err
		}
		rid = ar.RunID
		missions = append(missions, PlannedMission{
			MissionID: sm.MissionID,
			Prompt:    sm.Prompt,
			AttemptID: ar.AttemptID,
			OutDir:    ar.OutDir,
			OutDirAbs: ar.OutDirAbs,
			Env:       ar.Env,
		})
	}

	return SuitePlanResult{
		OK:        true,
		RunID:     rid,
		SuiteID:   parsed.Suite.SuiteID,
		Mode:      mode,
		OutRoot:   opts.OutRoot,
		Missions:  missions,
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
	}, nil
}
