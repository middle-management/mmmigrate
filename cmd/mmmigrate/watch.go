package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/source"
)

func cmdWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	databaseURL := fs.String("database-url", "", "Database connection URL (defaults to DATABASE_URL env var)")
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	debounce := fs.Duration("debounce", 200*time.Millisecond, "Debounce window for file change events")
	fs.Parse(args)

	absDir := resolveDir(*migrationsDir)
	db, cleanup := openDB(*databaseURL)
	defer cleanup()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runWatch(ctx, db, absDir, *debounce); err != nil && !errors.Is(err, context.Canceled) {
		fatal("%v", err)
	}
}

func runWatch(ctx context.Context, db *sql.DB, absDir string, debounce time.Duration) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}
	defer watcher.Close()

	// Watch the migrations directory itself so we capture atomic-rename saves,
	// plus any subdirectories that contain included files.
	watchedDirs := map[string]bool{}
	paths, err := discoverWatchPaths(absDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	if err := syncWatchedDirs(watcher, watchedDirs, absDir, paths); err != nil {
		return err
	}

	fmt.Printf("Watching %s (current.sql + %d include(s), debounce %s)\n",
		absDir, len(paths)-1, debounce)

	apply := func() {
		if err := mmmigrate.RunMigrations(ctx, db, dialect, absDir, true); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] Error: %v\n", time.Now().Format("15:04:05"), err)
		} else {
			fmt.Printf("[%s] ✓ applied\n", time.Now().Format("15:04:05"))
		}

		// Always re-discover includes, even on apply error: the user may have
		// added a new @include that we need to watch so their next save fires.
		newPaths, err := discoverWatchPaths(absDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		paths = newPaths
		if err := syncWatchedDirs(watcher, watchedDirs, absDir, paths); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
	}

	// Initial apply so the user sees current state before editing.
	apply()

	var timer *time.Timer
	trigger := make(chan struct{}, 1)

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nStopped.")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// Only react to events on files we care about.
			if !paths[event.Name] {
				continue
			}
			// Coalesce bursts (editors often emit multiple events per save).
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounce, func() {
				select {
				case trigger <- struct{}{}:
				default:
				}
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)

		case <-trigger:
			apply()
		}
	}
}

// discoverWatchPaths returns the absolute paths of current.sql plus every file
// it transitively @includes. A missing current.sql is not an error (the user
// may be about to create it); we still watch for its creation.
func discoverWatchPaths(absDir string) (map[string]bool, error) {
	paths := map[string]bool{
		filepath.Join(absDir, "current.sql"): true,
	}

	content, err := os.ReadFile(filepath.Join(absDir, "current.sql"))
	if err != nil {
		if os.IsNotExist(err) {
			return paths, nil
		}
		return paths, fmt.Errorf("failed to read current.sql: %w", err)
	}

	_, infos, err := source.ProcessIncludes(string(content), absDir)
	if err != nil {
		return paths, fmt.Errorf("failed to process includes: %w", err)
	}

	for _, info := range infos {
		paths[filepath.Join(absDir, info.Path)] = true
	}
	return paths, nil
}

// syncWatchedDirs ensures the watcher is watching every directory that
// contains a file we care about. We watch directories (not files) so that
// atomic-rename saves from editors don't detach the watch.
func syncWatchedDirs(watcher *fsnotify.Watcher, watched map[string]bool, absDir string, paths map[string]bool) error {
	want := map[string]bool{absDir: true}
	for p := range paths {
		want[filepath.Dir(p)] = true
	}

	for dir := range want {
		if watched[dir] {
			continue
		}
		if err := watcher.Add(dir); err != nil {
			return fmt.Errorf("failed to watch %s: %w", dir, err)
		}
		watched[dir] = true
	}
	for dir := range watched {
		if want[dir] {
			continue
		}
		_ = watcher.Remove(dir)
		delete(watched, dir)
	}
	return nil
}
