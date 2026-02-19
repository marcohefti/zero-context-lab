package schema

const (
	// IsolationModelProcessRunnerV1 means the attempt was executed via an external process runner.
	IsolationModelProcessRunnerV1 = "process_runner"
	// IsolationModelNativeSpawnV1 means the host orchestrator spawned a native fresh agent/session.
	IsolationModelNativeSpawnV1 = "native_spawn"
)

func IsValidIsolationModelV1(v string) bool {
	switch v {
	case "", IsolationModelProcessRunnerV1, IsolationModelNativeSpawnV1:
		return true
	default:
		return false
	}
}
