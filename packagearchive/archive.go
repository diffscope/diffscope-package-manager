package packagearchive

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"
)

type Limits struct {
	Entries               int
	FileBytes, TotalBytes int64
	PathBytes             int
	CompressionRatio      uint64
}

func DefaultLimits() Limits { return Limits{100_000, 64 << 30, 256 << 30, 4096, 1000} }
func (l Limits) validate() error {
	if l.Entries <= 0 || l.FileBytes <= 0 || l.TotalBytes <= 0 || l.PathBytes <= 0 || l.CompressionRatio == 0 {
		return fmt.Errorf("archive limits must be positive")
	}
	return nil
}

type Archive struct {
	file   *os.File
	files  map[string]*zip.File
	reader *zip.Reader
	limits Limits
}

func Open(filename string, limits Limits) (*Archive, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	initial, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	if !initial.Mode().IsRegular() {
		return nil, fmt.Errorf("package archive must be a regular file")
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	z, err := zip.NewReader(f, st.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	a := &Archive{file: f, files: map[string]*zip.File{}, reader: z, limits: limits}
	if err = a.preflight(); err != nil {
		f.Close()
		return nil, err
	}
	return a, nil
}
func (a *Archive) Close() error { return a.file.Close() }

// Files is a bounded file view suitable for the package metadata loader.
func (a *Archive) Files() fs.FS { return archiveFS{a} }

type archiveFS struct{ a *Archive }

func (v archiveFS) Open(name string) (fs.File, error) {
	f, ok := v.a.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	return &entryReader{ReadCloser: r, info: f.FileInfo(), remaining: v.a.limits.FileBytes}, nil
}

type entryReader struct {
	io.ReadCloser
	info      fs.FileInfo
	remaining int64
}

func (f *entryReader) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *entryReader) Read(b []byte) (int, error) {
	if f.remaining < int64(len(b))-1 {
		b = b[:f.remaining+1]
	}
	n, err := f.ReadCloser.Read(b)
	f.remaining -= int64(n)
	if f.remaining < 0 {
		return n, fmt.Errorf("archive file size limit exceeded")
	}
	return n, err
}
func validateName(name string, dir bool, limit int) error {
	if len(name) > limit || !utf8.ValidString(name) || strings.ContainsAny(name, "\\\x00:") {
		return fmt.Errorf("invalid archive path %q", name)
	}
	if dir && !strings.HasSuffix(name, "/") {
		return fmt.Errorf("directory entry requires trailing slash")
	}
	if dir {
		name = strings.TrimSuffix(name, "/")
	}
	if !fs.ValidPath(name) || name == "." {
		return fmt.Errorf("invalid archive path %q", name)
	}
	for _, p := range strings.Split(name, "/") {
		if strings.TrimRight(p, ". ") != p || strings.ContainsAny(p, "<>\"|?*") {
			return fmt.Errorf("nonportable archive path %q", name)
		}
		for _, r := range p {
			if r < 32 {
				return fmt.Errorf("control character in archive path")
			}
		}
		base := strings.ToUpper(strings.SplitN(p, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
			return fmt.Errorf("reserved archive path %q", name)
		}
	}
	return nil
}
func (a *Archive) preflight() error {
	if len(a.reader.File) > a.limits.Entries {
		return fmt.Errorf("too many archive entries")
	}
	entries := map[string]bool{}
	names := map[string]string{}
	kinds := map[string]bool{}
	var total uint64
	for _, f := range a.reader.File {
		mode := f.Mode()
		creator := f.CreatorVersion >> 8
		if (creator == 0 || creator == 10) && f.ExternalAttrs&0x400 != 0 {
			return fmt.Errorf("reparse archive entry %q", f.Name)
		}
		dir := mode.IsDir()
		if !mode.IsRegular() && !dir {
			return fmt.Errorf("special archive entry %q", f.Name)
		}
		if err := validateName(f.Name, dir, a.limits.PathBytes); err != nil {
			return err
		}
		if entries[f.Name] {
			return fmt.Errorf("duplicate archive entry %q", f.Name)
		}
		entries[f.Name] = true
		name := strings.TrimSuffix(f.Name, "/")
		parts := strings.Split(name, "/")
		for i := range parts {
			p := strings.Join(parts[:i+1], "/")
			key := strings.ToLower(p)
			isDir := i < len(parts)-1 || dir
			if prior, exists := names[key]; exists && prior != p {
				return fmt.Errorf("case collision: %q and %q", prior, p)
			}
			names[key] = p
			if prior, exists := kinds[p]; exists && prior != isDir {
				return fmt.Errorf("file/directory collision %q", p)
			}
			kinds[p] = isDir
			if len(kinds) > a.limits.Entries {
				return fmt.Errorf("too many installed archive entries, including implicit directories")
			}
		}
		if dir {
			continue
		}
		if strings.HasSuffix(f.Name, "/") {
			return fmt.Errorf("file entry has directory name")
		}
		size := f.UncompressedSize64
		if size > uint64(a.limits.FileBytes) || size > uint64(a.limits.TotalBytes)-total {
			return fmt.Errorf("archive size limit exceeded")
		}
		total += size
		if size > 0 && (f.CompressedSize64 == 0 || size/f.CompressedSize64 > a.limits.CompressionRatio || size/f.CompressedSize64 == a.limits.CompressionRatio && size%f.CompressedSize64 > 0) {
			return fmt.Errorf("archive compression ratio limit exceeded")
		}
		a.files[f.Name] = f
	}
	if _, ok := a.files["desc.json"]; !ok {
		return fmt.Errorf("archive requires root desc.json")
	}
	return nil
}
