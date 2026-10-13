package commands

import (
	"github.com/diffscope/diffscope-package-manager/internal/config"
	"github.com/diffscope/diffscope-package-manager/internal/downloadcache"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/diffscope/diffscope-package-manager/internal/repositorycreate"
	"github.com/spf13/cobra"
	"strings"
	"time"
)

func commandName(c *cobra.Command) string {
	return strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
}
func remoteSettings(cmd *cobra.Command, o *Options) (config.Remote, error) {
	if o.Progress != "auto" && o.Progress != "plain" && o.Progress != "none" {
		return config.Remote{}, &packagemanager.Error{Code: "INVALID_ARGUMENT", Message: "The progress mode must be auto, plain, or none."}
	}
	r, err := config.LoadRemote(o.ConfigFile, o.Registries, cmd.Flags().Changed("registry"), o.CacheDir, cmd.Flags().Changed("cache-dir"), o.Concurrency, cmd.Flags().Changed("download-concurrency"))
	if err != nil {
		return r, &packagemanager.Error{Code: "INVALID_ARGUMENT", Message: err.Error(), Cause: err}
	}
	r.Offline = o.Offline
	if cmd.Flags().Changed("download-timeout") {
		if o.DownloadTimeout < time.Second || o.DownloadTimeout%time.Second != 0 {
			return r, &packagemanager.Error{Code: "INVALID_ARGUMENT", Message: "The download timeout must be a positive whole number of seconds."}
		}
		r.DownloadTimeoutSeconds = int(o.DownloadTimeout / time.Second)
	}
	if cmd.Flags().Changed("download-retries") {
		r.DownloadRetries = o.DownloadRetries
	}
	if r.DownloadTimeoutSeconds < 1 || r.DownloadRetries < 0 {
		return r, &packagemanager.Error{Code: "INVALID_ARGUMENT", Message: "The download timeout must be positive and the retry count must be nonnegative."}
	}
	if int64(r.DownloadTimeoutSeconds) > int64((1<<63-1)/time.Second) {
		return r, &packagemanager.Error{Code: "INVALID_ARGUMENT", Message: "The download timeout exceeds the supported duration."}
	}
	return r, nil
}
func commandError(err error) error {
	if err == nil {
		return nil
	}
	code := "IO_ERROR"
	for _, c := range []string{"CONFLICT", "INVALID_CACHE_DIRECTORY", "CACHE_BUSY", "REPOSITORY_RECOVERY_REQUIRED", "INPUT_CHANGED", "NONPORTABLE_METADATA"} {
		if strings.Contains(err.Error(), c) {
			code = c
			break
		}
	}
	return &packagemanager.Error{Code: code, Message: err.Error(), Cause: err}
}
func repository(o *Options) *cobra.Command {
	parent := &cobra.Command{Use: "repo", Short: "Repository publication data"}
	var file, root, url string
	var dry bool
	c := &cobra.Command{Use: "create", Short: "OVCPR v1 submission generation", Args: cobra.NoArgs}
	c.Flags().StringVar(&file, "file", "", "Select the exact DSPK archive.")
	c.Flags().StringVar(&root, "repository", "", "Select an existing repository root for package data.")
	c.Flags().StringVar(&url, "download-url", "", "Specify the public HTTPS archive URL. Generation does not contact this URL.")
	c.Flags().BoolVar(&dry, "dry-run", false, "Validate and preview without writing.")
	_ = c.MarkFlagRequired("file")
	_ = c.MarkFlagRequired("repository")
	_ = c.MarkFlagRequired("download-url")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r := renderer(cmd, o, true)
		defer r.Close()
		result, err := repositorycreate.Create(cmd.Context(), file, root, url, dry)
		if err != nil {
			return r.Finish(commandError(err))
		}
		r.Observer(packagemanager.Event{Event: "plan", Data: result})
		if !dry {
			status := "created"
			if !result.Changed {
				status = "unchanged"
			}
			r.Observer(packagemanager.Event{Event: "result", Data: map[string]any{"status": status, "manifestPath": result.ManifestPath}})
		}
		r.Observer(packagemanager.Event{Event: "summary", Data: result})
		return r.Finish(nil)
	}
	parent.AddCommand(c)
	return parent
}
func cache(o *Options) *cobra.Command {
	parent := &cobra.Command{Use: "cache", Short: "Download cache management"}
	for _, name := range []string{"dir", "info"} {
		name := name
		c := &cobra.Command{Use: name, Args: cobra.NoArgs}
		c.RunE = func(cmd *cobra.Command, args []string) error {
			r := renderer(cmd, o, false)
			defer r.Close()
			settings, err := remoteSettings(cmd, o)
			if err != nil {
				return r.Finish(err)
			}
			if name == "dir" {
				return r.Query(map[string]any{"directory": settings.CacheDir}, nil)
			}
			store, err := downloadcache.Open(cmd.Context(), settings.CacheDir, o.PackagesDir, false)
			if err != nil {
				return r.Finish(commandError(err))
			}
			if store == nil {
				return r.Query(downloadcache.Stats{Directory: settings.CacheDir, Files: map[string]int{}, Bytes: map[string]int64{}}, nil)
			}
			defer store.Close()
			stats, err := store.Stats()
			if err != nil {
				return r.Finish(err)
			}
			return r.Query(stats, nil)
		}
		parent.AddCommand(c)
	}
	var downloads, metadata, partial, temporary, dry bool
	c := &cobra.Command{Use: "clean", Short: "Managed cache cleanup", Args: cobra.NoArgs}
	c.Flags().BoolVar(&downloads, "downloads", false, "Limit cleanup to archives and partial downloads.")
	c.Flags().BoolVar(&metadata, "metadata", false, "Limit cleanup to repository metadata.")
	c.Flags().BoolVar(&partial, "partial", false, "Limit cleanup to resumable partial downloads.")
	c.Flags().BoolVar(&dry, "dry-run", false, "Preview managed deletions without deleting files.")
	c.Flags().BoolVar(&temporary, "temporary", false, "Limit cleanup to incomplete temporary writes and nonresumable transfers.")
	c.MarkFlagsMutuallyExclusive("downloads", "metadata", "partial", "temporary")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r := renderer(cmd, o, true)
		defer r.Close()
		settings, err := remoteSettings(cmd, o)
		if err != nil {
			return r.Finish(err)
		}
		scope := "all"
		if temporary {
			scope = "temporary"
		}
		if downloads {
			scope = "downloads"
		}
		if metadata {
			scope = "metadata"
		}
		if partial {
			scope = "partial"
		}
		store, err := downloadcache.Open(cmd.Context(), settings.CacheDir, o.PackagesDir, false)
		if err != nil {
			return r.Finish(commandError(err))
		}
		result := downloadcache.CleanResult{Scope: scope, DryRun: dry}
		if store != nil {
			defer store.Close()
			result, err = store.Clean(cmd.Context(), scope, dry)
		}
		if err != nil {
			return r.Finish(commandError(err))
		}
		r.Observer(packagemanager.Event{Event: "plan", Data: result})
		if !dry {
			r.Observer(packagemanager.Event{Event: "result", Data: result})
		}
		r.Observer(packagemanager.Event{Event: "summary", Data: result})
		return r.Finish(nil)
	}
	parent.AddCommand(c)
	return parent
}
