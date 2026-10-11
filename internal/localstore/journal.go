package localstore

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validStage(s string) bool {
	return strings.HasPrefix(s, "stage-") && digits(strings.TrimPrefix(s, "stage-"))
}
func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func validTx(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 16 && s == strings.ToLower(s)
}
func (s *Session) writeJournal(j journal) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if len(b) > 16<<20 {
		return fmt.Errorf("transaction journal exceeds limit")
	}
	base := filepath.Join(s.txRoot(), j.ID)
	f, err := os.OpenFile(filepath.Join(base, "journal.tmp"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(b)
	syncErr := f.Sync()
	closeErr := f.Close()
	for _, e := range []error{writeErr, syncErr, closeErr} {
		if e != nil {
			return e
		}
	}
	return os.Rename(filepath.Join(base, "journal.tmp"), filepath.Join(base, "journal.json"))
}
