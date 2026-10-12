package packagearchive

// Returns the total regular file size validated during archive preflight.
func (a *Archive) UncompressedBytes() int64 {
	var n int64
	for _, f := range a.files {
		n += int64(f.UncompressedSize64)
	}
	return n
}
