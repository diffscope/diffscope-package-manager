package commands

import (
	"testing"
	"time"
)

func TestFormatInstalledAtTextUsesLocalUnixDateWithMilliseconds(t *testing.T) {
	got := formatInstalledAtText(2345)
	want := time.UnixMilli(2345).Format("Mon Jan _2 15:04:05.000 MST 2006")
	if got != want {
		t.Fatalf("formatInstalledAtText() = %q, want %q", got, want)
	}
}

func TestFormatInstalledAtJSONUsesUTCMilliseconds(t *testing.T) {
	got := formatInstalledAtJSON(2345)
	if got != "1970-01-01T00:00:02.345Z" {
		t.Fatalf("formatInstalledAtJSON() = %q, want UTC millisecond timestamp", got)
	}
}
