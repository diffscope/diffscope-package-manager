package localstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"os"
	"path/filepath"
)

func (s *Session) txRoot() string { return filepath.Join(s.Root, ".dspm", "transactions") }
func (s *Session) ensureTransaction() error {
	if !s.writable {
		return fmt.Errorf("read session cannot modify packages")
	}
	if s.transaction != "" {
		return nil
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	s.transaction = hex.EncodeToString(id[:])
	if err := os.MkdirAll(s.txRoot(), 0o755); err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(s.txRoot(), s.transaction), 0o700)
}
func (s *Session) Stage(ctx context.Context, a *packagearchive.Archive) (string, packageinfo.PackageManifest, error) {
	return s.StageWithProgress(ctx, a, nil)
}

func (s *Session) StageWithProgress(ctx context.Context, a *packagearchive.Archive, progress packagearchive.ProgressFunc) (string, packageinfo.PackageManifest, error) {
	var p packageinfo.PackageManifest
	if err := s.ensureTransaction(); err != nil {
		return "", p, err
	}
	stage := fmt.Sprintf("stage-%d", s.stageCount)
	s.stageCount++
	dest := filepath.Join(s.txRoot(), s.transaction, stage)
	if err := a.ExtractWithProgress(ctx, dest, progress); err != nil {
		return "", p, err
	}
	d, err := packagearchive.OpenDirectory(dest)
	if err != nil {
		return "", p, err
	}
	defer d.Close()
	p, err = packageinfo.Load(d, dest)
	return stage, p, err
}
func (s *Session) StagedHash(ctx context.Context, stage string, limits packagearchive.Limits) (string, error) {
	return s.StagedHashWithProgress(ctx, stage, limits, nil)
}

func (s *Session) StagedHashWithProgress(ctx context.Context, stage string, limits packagearchive.Limits, progress packagearchive.ProgressFunc) (string, error) {
	if !validStage(stage) || s.transaction == "" {
		return "", fmt.Errorf("invalid stage")
	}
	return packagearchive.HashDirectoryWithProgress(ctx, filepath.Join(s.txRoot(), s.transaction, stage), limits, progress)
}
func (s *Session) LoadStage(stage string, id packageinfo.Identity) (packageinfo.PackageManifest, error) {
	if !validStage(stage) || s.transaction == "" {
		return packageinfo.PackageManifest{}, fmt.Errorf("invalid stage")
	}
	d, err := packagearchive.OpenDirectory(filepath.Join(s.txRoot(), s.transaction, stage))
	if err != nil {
		return packageinfo.PackageManifest{}, err
	}
	defer d.Close()
	return packageinfo.Load(d, filepath.Join(s.Root, DirectoryName(id)))
}
