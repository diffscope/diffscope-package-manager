package packagemanager

import "github.com/diffscope/diffscope-package-manager/packagearchive"

// Progress is independent of terminal rendering. Mutations expose the same
// observations to interactive users and NDJSON consumers.
type Progress struct {
	Phase  string `json:"phase"`
	Target string `json:"target"`
	State  string `json:"state"`
	packagearchive.Progress
}

func beginProgress(observer Observer, phase, target string) (packagearchive.ProgressFunc, func()) {
	p := Progress{Phase: phase, Target: target, State: "start"}
	emit(observer, "progress", p)
	return func(update packagearchive.Progress) {
			p.Progress = update
			p.State = "update"
			emit(observer, "progress", p)
		}, func() {
			p.State = "done"
			emit(observer, "progress", p)
		}
}
