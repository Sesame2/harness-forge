package profiles

type Snapshot struct {
	ID                      string
	Version                 string
	DisplayName             string
	SystemPrompt            string
	AllowedTools            []string
	PermissionMode          string
	Agent                   AgentPolicy
	AcceptedInputMediaTypes []string
	Artifacts               ArtifactPolicy
	WorkspaceTemplate       []WorkspaceFile
	Digest                  string
}

type AgentPolicy struct {
	MaxTurns     int
	MaxBudgetUSD float64
}

type ArtifactPolicy struct {
	ManifestSchemaVersion int
	AllowedTypes          []string
	MaxFileBytes          int64
	MaxTotalBytes         int64
}

type WorkspaceFile struct {
	Path    string
	Content []byte
}
