package packagemanager

import (
	"context"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/diffscope/diffscope-package-manager/internal/config"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
)

type DownloadRequest struct {
	Targets      []string
	Directory    string
	DryRun       bool
	Settings     config.Remote
	Unrestricted bool
}

// Downloads verified archives and their complete dependency collection without accessing the installation database.
// Returns exported file actions or an error for resolution, acquisition, validation, or export failure.
func (m *Manager) Download(ctx context.Context, request DownloadRequest, observer Observer) (MutationResult, error) {
	directory := request.Directory
	if directory == "" {
		directory = "."
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return MutationResult{}, err
	}
	return m.acquireRemote(ctx, RemoteRequest{Targets: request.Targets, DryRun: request.DryRun, Settings: request.Settings, Unrestricted: request.Unrestricted}, directory, observer)
}

type exportWriter struct {
	ctx context.Context
	io.Writer
}

func (w exportWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.Writer.Write(p)
}

func exportArchive(ctx context.Context, directory string, input installInput, manifest repo.RemoteManifest) (Action, error) {
	name := path.Join(manifest.ID, manifest.Version.String()+".dspk")
	action := Action{Package: manifest.Identity, Action: "download", File: filepath.Join(directory, filepath.FromSlash(name)), Hash: input.hash}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return action, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return action, err
	}
	defer root.Close()
	if old, err := root.Open(name); err == nil {
		st, statErr := old.Stat()
		var digest string
		if statErr == nil {
			digest, statErr = packagearchive.HashFileBytes(ctx, old, st.Size(), nil)
		}
		old.Close()
		if statErr != nil {
			return action, statErr
		}
		if digest != manifest.DownloadSha512 {
			return action, failure("CONFLICT", fmt.Errorf("The destination contains a different archive: %s.", action.File))
		}
		action.Action = "skip"
		return action, nil
	} else if !os.IsNotExist(err) {
		return action, err
	}
	if err = root.MkdirAll(path.Dir(name), 0755); err != nil {
		return action, err
	}
	// Exclusive temporary files and links prevent replacement of existing exports.
	temporary := name + "." + rand.Text() + ".tmp"
	output, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return action, err
	}
	defer root.Remove(temporary)
	source, err := os.Open(input.file)
	if err != nil {
		output.Close()
		return action, err
	}
	defer source.Close()
	digest := sha512.New()
	n, copyErr := io.Copy(exportWriter{ctx, io.MultiWriter(output, digest)}, source)
	syncErr := output.Sync()
	closeErr := output.Close()
	for _, err := range []error{copyErr, syncErr, closeErr} {
		if err != nil {
			return action, err
		}
	}
	if n != manifest.DownloadSize || hex.EncodeToString(digest.Sum(nil)) != manifest.DownloadSha512 {
		return action, failure("INPUT_CHANGED", fmt.Errorf("The archive changed during export: %s.", manifest.String()))
	}
	if err = root.Link(temporary, name); err != nil {
		return action, err
	}
	return action, nil
}
