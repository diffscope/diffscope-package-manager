package packagearchive

import (
	"archive/zip"
	"context"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type PackPlan struct {
	Source      string                      `json:"source"`
	Output      string                      `json:"output"`
	Files       []string                    `json:"files"`
	Directories []string                    `json:"directories"`
	Bytes       int64                       `json:"bytes"`
	Package     packageinfo.PackageManifest `json:"package"`
}

func Plan(ctx context.Context, source, output string, limits Limits) (PackPlan, error) {
	var p PackPlan
	if err := limits.validate(); err != nil {
		return p, err
	}
	var err error
	p.Source, err = filepath.Abs(source)
	if err != nil {
		return p, err
	}
	p.Output, err = filepath.Abs(output)
	if err != nil {
		return p, err
	}
	sourceInfo, err := os.Lstat(p.Source)
	if err != nil {
		return p, err
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return p, fmt.Errorf("source must be an ordinary directory")
	}
	outputInfo, _ := os.Stat(p.Output)
	d, err := OpenDirectory(p.Source)
	if err != nil {
		return p, err
	}
	defer d.Close()
	p.Package, err = packageinfo.Load(d, p.Source)
	if err != nil {
		return p, err
	}
	seen := map[string]string{}
	err = filepath.WalkDir(p.Source, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filename == p.Source {
			return nil
		}
		if filename == p.Output {
			return nil
		}
		relative, err := filepath.Rel(p.Source, filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source contains link %q", name)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if outputInfo != nil && strings.EqualFold(filename, p.Output) && os.SameFile(info, outputInfo) {
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("source contains special file %q", name)
		}
		checkName := name
		if info.IsDir() {
			checkName += "/"
		}
		if err := validateName(checkName, info.IsDir(), limits.PathBytes); err != nil {
			return err
		}
		key := strings.ToLower(name)
		if prior, ok := seen[key]; ok && prior != name {
			return fmt.Errorf("case collision in source")
		}
		seen[key] = name
		if len(seen) > limits.Entries {
			return fmt.Errorf("too many source entries")
		}
		if info.IsDir() {
			p.Directories = append(p.Directories, name)
			return nil
		}
		if info.Size() > limits.FileBytes || info.Size() > limits.TotalBytes-p.Bytes {
			return fmt.Errorf("source size limit exceeded")
		}
		p.Bytes += info.Size()
		p.Files = append(p.Files, name)
		return nil
	})
	sort.Strings(p.Files)
	sort.Strings(p.Directories)
	return p, err
}
func Pack(ctx context.Context, p PackPlan, force bool, limits Limits) (string, error) {
	return PackWithProgress(ctx, p, force, limits, nil, nil)
}

// PackWithProgress reports writing and subsequent archive verification separately.
func PackWithProgress(ctx context.Context, p PackPlan, force bool, limits Limits, packing, verification ProgressFunc) (string, error) {
	temp, err := os.CreateTemp(filepath.Dir(p.Output), ".dspm-pack-*.tmp")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	d, err := OpenDirectory(p.Source)
	if err != nil {
		temp.Close()
		return "", err
	}
	defer d.Close()
	z := zip.NewWriter(temp)
	for _, name := range p.Directories {
		header := &zip.FileHeader{Name: name + "/", Method: zip.Store}
		header.SetMode(os.ModeDir | 0o755)
		if _, err := z.CreateHeader(header); err != nil {
			z.Close()
			temp.Close()
			return "", err
		}
	}
	var total int64
	progress := newProgress(p.Bytes, len(p.Files), packing)
	for _, name := range p.Files {
		progress.file(name)
		f, err := d.Open(name)
		if err != nil {
			z.Close()
			temp.Close()
			return "", err
		}
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			f.Close()
			z.Close()
			temp.Close()
			return "", fmt.Errorf("source changed: %s", name)
		}
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err == nil {
			var n int64
			n, err = copyContextWithProgress(ctx, w, f, min(limits.FileBytes, limits.TotalBytes-total), progress.add)
			total += n
		}
		f.Close()
		if err != nil {
			z.Close()
			temp.Close()
			return "", err
		}
		progress.done()
	}
	if total != p.Bytes {
		z.Close()
		temp.Close()
		return "", fmt.Errorf("source size changed while packing")
	}
	if err = z.Close(); err != nil {
		temp.Close()
		return "", err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return "", err
	}
	if err = temp.Close(); err != nil {
		return "", err
	}
	a, err := Open(tempName, limits)
	if err != nil {
		return "", err
	}
	_, loadErr := packageinfo.Load(a.Files(), p.Source)
	hash, hashErr := a.HashWithProgress(ctx, verification)
	closeErr := a.Close()
	for _, err := range []error{loadErr, hashErr, closeErr} {
		if err != nil {
			return "", err
		}
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if force {
		err = os.Rename(tempName, p.Output)
	} else {
		err = os.Link(tempName, p.Output)
	}
	if err != nil {
		return "", err
	}
	return hash, nil
}
