package main

// Hands a game to OnionOS exactly the way Onion's own Random Game app does:
// write .tmp_update/cmd_to_run.sh, add the game to the Recents list, then exit.
// launch.sh touches /tmp/quick_switch so Onion's runtime starts the game
// instead of returning to MainUI.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ldPreload = "LD_PRELOAD=/mnt/SDCARD/miyoo/app/../lib/libpadsp.so"

func LaunchCommand(r *Rom) string {
	return fmt.Sprintf("%s \"%s\" \"%s\"", ldPreload, r.Sys.Launch, r.Path)
}

// QueueLaunch writes the command for Onion's runtime and the marker launch.sh
// looks for. It does not start the game itself.
func QueueLaunch(env *Env, r *Rom) error {
	sysdir := filepath.Join(env.SDRoot, ".tmp_update")
	if err := writeSync(filepath.Join(sysdir, "cmd_to_run.sh"), LaunchCommand(r)); err != nil {
		return err
	}
	addRecent(env, r)
	return writeSync(filepath.Join(env.DataDir, ".launch"), r.Path)
}

// addRecent puts the game at the top of Onion's Recents list (and so the
// GameSwitcher), mirroring random.sh.
func addRecent(env *Env, r *Rom) {
	list := filepath.Join(env.SDRoot, "Roms", "recentlist.json")
	if _, err := os.Stat(filepath.Join(env.SDRoot, ".tmp_update", "config", ".showRecents")); err != nil {
		list = filepath.Join(env.SDRoot, "Roms", "recentlist-hidden.json")
	}
	entry := map[string]any{"label": r.Label, "rompath": r.Path, "launch": r.Sys.Launch, "type": 5}
	if img := r.BoxArt(); img != "" {
		entry["imgpath"] = img
	}
	line, _ := json.Marshal(entry)
	old, _ := os.ReadFile(list)
	// drop an older entry for the same game so it isn't listed twice
	var keep []string
	for _, ln := range strings.Split(string(old), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var e struct {
			Rompath string `json:"rompath"`
		}
		if json.Unmarshal([]byte(ln), &e) == nil && e.Rompath == r.Path {
			continue
		}
		keep = append(keep, ln)
	}
	out := string(line) + "\n"
	if len(keep) > 0 {
		out += strings.Join(keep, "\n") + "\n"
	}
	writeSync(list, out)
}

func writeSync(path, s string) error {
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(s); err != nil {
		f.Close()
		return err
	}
	f.Sync()
	return f.Close()
}
