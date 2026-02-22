package runners

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/campaign"
)

type FlowMissionInvoker func(ctx context.Context, flow campaign.FlowSpec, missionIndex int, missionID string) (campaign.FlowRunV1, error)

type FlowAdapter interface {
	Prepare(ctx context.Context, flow campaign.FlowSpec) error
	RunMission(ctx context.Context, flow campaign.FlowSpec, missionIndex int, missionID string) (campaign.FlowRunV1, error)
	Cleanup(ctx context.Context, flow campaign.FlowSpec) error
}

type CampaignExecutor struct {
	adapters map[string]FlowAdapter
}

func NewCampaignExecutor(invoker FlowMissionInvoker) (*CampaignExecutor, error) {
	if invoker == nil {
		return nil, fmt.Errorf("missing flow mission invoker")
	}
	mk := func(kind string) FlowAdapter {
		return &suiteRunFlowAdapter{kind: kind, invoker: invoker}
	}
	return &CampaignExecutor{adapters: map[string]FlowAdapter{
		campaign.RunnerTypeProcessCmd:  mk(campaign.RunnerTypeProcessCmd),
		campaign.RunnerTypeCodexExec:   mk(campaign.RunnerTypeCodexExec),
		campaign.RunnerTypeCodexSub:    mk(campaign.RunnerTypeCodexSub),
		campaign.RunnerTypeClaudeSub:   mk(campaign.RunnerTypeClaudeSub),
		campaign.RunnerTypeCodexAppSrv: mk(campaign.RunnerTypeCodexAppSrv),
	}}, nil
}

func (c *CampaignExecutor) Prepare(ctx context.Context, flow campaign.FlowSpec) error {
	adapter, err := c.adapterFor(flow)
	if err != nil {
		return err
	}
	return adapter.Prepare(ctx, flow)
}

func (c *CampaignExecutor) RunMission(ctx context.Context, flow campaign.FlowSpec, missionIndex int, missionID string) (campaign.FlowRunV1, error) {
	adapter, err := c.adapterFor(flow)
	if err != nil {
		return campaign.FlowRunV1{}, err
	}
	result, runErr := adapter.RunMission(ctx, flow, missionIndex, missionID)
	result.FlowID = flow.FlowID
	result.RunnerType = flow.Runner.Type
	result.SuiteFile = flow.SuiteFile
	normalizeAttemptContract(&result)
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}

func (c *CampaignExecutor) Cleanup(ctx context.Context, flow campaign.FlowSpec) error {
	adapter, err := c.adapterFor(flow)
	if err != nil {
		return err
	}
	return adapter.Cleanup(ctx, flow)
}

func (c *CampaignExecutor) adapterFor(flow campaign.FlowSpec) (FlowAdapter, error) {
	if c == nil {
		return nil, fmt.Errorf("campaign executor is nil")
	}
	kind := strings.TrimSpace(strings.ToLower(flow.Runner.Type))
	adapter, ok := c.adapters[kind]
	if !ok {
		return nil, fmt.Errorf("unsupported campaign runner type %q", flow.Runner.Type)
	}
	return adapter, nil
}

type suiteRunFlowAdapter struct {
	kind    string
	invoker FlowMissionInvoker
}

func (a *suiteRunFlowAdapter) Prepare(_ context.Context, _ campaign.FlowSpec) error {
	if a.invoker == nil {
		return fmt.Errorf("missing invoker")
	}
	return nil
}

func (a *suiteRunFlowAdapter) RunMission(ctx context.Context, flow campaign.FlowSpec, missionIndex int, missionID string) (campaign.FlowRunV1, error) {
	if a.invoker == nil {
		return campaign.FlowRunV1{}, fmt.Errorf("missing invoker")
	}
	result, err := a.invoker(ctx, flow, missionIndex, missionID)
	result.RunnerType = a.kind
	return result, err
}

func (a *suiteRunFlowAdapter) Cleanup(_ context.Context, _ campaign.FlowSpec) error {
	return nil
}

func normalizeAttemptContract(fr *campaign.FlowRunV1) {
	if fr == nil {
		return
	}
	for i := range fr.Attempts {
		a := &fr.Attempts[i]
		a.AttemptDir = strings.TrimSpace(a.AttemptDir)
		a.AttemptID = strings.TrimSpace(a.AttemptID)
		if strings.TrimSpace(a.Status) == "" {
			a.Status = campaign.AttemptStatusInvalid
		}
		a.Errors = dedupeSortedStrings(a.Errors)
	}
	fr.Errors = dedupeSortedStrings(fr.Errors)
}

func dedupeSortedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
