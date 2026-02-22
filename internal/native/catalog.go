package native

type StrategyDescriptor struct {
	ID           StrategyID   `json:"id"`
	Description  string       `json:"description"`
	Capabilities Capabilities `json:"capabilities"`
	Recommended  bool         `json:"recommended"`
}

func BuiltinStrategyCatalog() []StrategyDescriptor {
	return []StrategyDescriptor{
		{
			ID:          StrategyCodexAppServer,
			Description: "Codex app-server JSON-RPC runtime (stdio transport).",
			Capabilities: Capabilities{
				SupportsThreadStart:      true,
				SupportsTurnSteer:        true,
				SupportsInterrupt:        true,
				SupportsEventStream:      true,
				SupportsParallelSessions: true,
			},
			Recommended: true,
		},
		{
			ID:          StrategyProviderStub,
			Description: "Provider onboarding stub (documents unsupported control-plane/API gaps).",
			Capabilities: Capabilities{
				SupportsThreadStart:      false,
				SupportsTurnSteer:        false,
				SupportsInterrupt:        false,
				SupportsEventStream:      false,
				SupportsParallelSessions: false,
			},
			Recommended: false,
		},
	}
}
