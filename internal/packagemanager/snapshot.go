package packagemanager

import (
	"context"
	"encoding/json"
	"github.com/diffscope/diffscope-package-manager/internal/localstore"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"sort"
	"strings"
	"time"
)

func snapshotFingerprint(records []packagedatabase.Record) string {
	copyOf := append([]packagedatabase.Record{}, records...)
	sort.Slice(copyOf, func(i, j int) bool { return copyOf[i].String() < copyOf[j].String() })
	raw, _ := json.Marshal(copyOf)
	return repo.Sha512(raw)
}
func openStore(ctx context.Context, root string, write bool, observer Observer) (*localstore.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	waiting := false
	for {
		s, err := localstore.Open(root, write)
		if err == nil {
			if waiting {
				emit(observer, "progress", Progress{Phase: "lock-wait", Target: root, State: "done"})
			}
			return s, nil
		}
		if !strings.Contains(err.Error(), "locked") {
			return nil, err
		}
		if !waiting {
			emit(observer, "progress", Progress{Phase: "lock-wait", Target: root, State: "start"})
			waiting = true
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
