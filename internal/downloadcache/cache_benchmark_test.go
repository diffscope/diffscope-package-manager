package downloadcache

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func BenchmarkOpenHistoricalCache(b *testing.B) {
	for _, count := range []int{0, 20000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := filepath.Join(b.TempDir(), "cache")
			c, err := Open(context.Background(), root, "", true)
			if err != nil {
				b.Fatal(err)
			}
			if err = c.Root.MkdirAll("metadata/history", 0700); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < count; i++ {
				if err = c.Root.WriteFile(fmt.Sprintf("metadata/history/%d", i), nil, 0600); err != nil {
					b.Fatal(err)
				}
			}
			c.Close()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c, err = Open(context.Background(), root, "", false)
				if err != nil {
					b.Fatal(err)
				}
				c.Close()
			}
		})
	}
}
