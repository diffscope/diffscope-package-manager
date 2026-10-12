package packagearchive

// Progress describes bytes successfully processed in one operation. It is
// synchronous, includes intermediate chunks within large files, and excludes
// directory entries. A byte total does not imply that validation has succeeded.
type Progress struct {
	FilePath   string `json:"filePath"`
	Current    int64  `json:"current"`
	Total      int64  `json:"total"`
	FilesDone  int    `json:"filesDone"`
	FilesTotal int    `json:"filesTotal"`
}

type ProgressFunc func(Progress)

type progressTracker struct {
	Progress
	callback ProgressFunc
}

func newProgress(total int64, files int, callback ProgressFunc) *progressTracker {
	p := &progressTracker{Progress: Progress{Total: total, FilesTotal: files}, callback: callback}
	p.report()
	return p
}
func (p *progressTracker) report() {
	if p.callback != nil {
		p.callback(p.Progress)
	}
}
func (p *progressTracker) file(name string) {
	p.FilePath = name
	p.report()
}
func (p *progressTracker) add(n int64) { p.Current += n; p.report() }
func (p *progressTracker) done()       { p.FilesDone++; p.report() }

func (a *Archive) byteSize() int64 {
	var total int64
	for _, file := range a.files {
		total += int64(file.UncompressedSize64) // Archive preflight bounds this sum.
	}
	return total
}
