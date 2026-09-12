package agentexec

import "path"

// Paths are absolute Runtime-visible paths. Only the Provider maps local paths.
type Paths struct {
	Inputs    string `json:"inputs"`
	Workspace string `json:"workspace"`
	Outputs   string `json:"outputs"`
}

func (p Paths) Valid() bool {
	return path.IsAbs(p.Inputs) && path.IsAbs(p.Workspace) && path.IsAbs(p.Outputs)
}
