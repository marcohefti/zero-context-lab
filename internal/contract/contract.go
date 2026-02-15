package contract

type Contract struct {
	Name                  string    `json:"name"`
	Version               string    `json:"version"`
	ArtifactLayoutVersion int       `json:"artifactLayoutVersion"`
	TraceSchemaVersion    int       `json:"traceSchemaVersion"`
	Commands              []Command `json:"commands"`
	Errors                []Error   `json:"errors"`
}

type Command struct {
	ID      string `json:"id"`
	Usage   string `json:"usage"`
	Summary string `json:"summary"`
}

type Error struct {
	Code      string `json:"code"`
	Summary   string `json:"summary"`
	Retryable bool   `json:"retryable"`
}

func Build(version string) Contract {
	return Contract{
		Name:                  "zcl",
		Version:               version,
		ArtifactLayoutVersion: 1,
		TraceSchemaVersion:    1,
		Commands: []Command{
			{
				ID:      "contract",
				Usage:   "zcl contract --json",
				Summary: "Print the ZCL surface contract (artifact layout + supported schema versions).",
			},
			{
				ID:      "attempt start",
				Usage:   "zcl attempt start --suite <suiteId> --mission <missionId> --json",
				Summary: "Allocate a run/attempt directory and print canonical IDs + env for the spawned agent.",
			},
		},
		Errors: []Error{
			{Code: "ZCL_E_USAGE", Summary: "Invalid CLI usage (missing/invalid flags).", Retryable: false},
			{Code: "ZCL_E_IO", Summary: "Filesystem I/O error while writing artifacts.", Retryable: true},
		},
	}
}
