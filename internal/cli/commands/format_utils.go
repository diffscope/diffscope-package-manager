package commands

import "time"

func formatInstalledAtJSON(installedAt int64) string {
	return time.UnixMilli(installedAt).UTC().Format("2006-01-02T15:04:05.000Z")
}

func formatInstalledAtText(installedAt int64) string {
	return time.UnixMilli(installedAt).Format("Mon Jan _2 15:04:05.000 MST 2006")
}
