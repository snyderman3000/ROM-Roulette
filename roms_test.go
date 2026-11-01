package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitTags(t *testing.T) {
	cases := map[string]string{
		"Pokemon - Emerald Version (USA, Europe)":         "Pokemon - Emerald Version",
		"Legend of Zelda, The - A Link to the Past (USA)": "The Legend of Zelda - A Link to the Past",
		"Minish Cap, The (USA)":                           "The Minish Cap",
		"(Prototype)":                                     "(Prototype)",
		"sf2":                                             "sf2",
	}
	for in, want := range cases {
		if got, _ := splitTags(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	_, tags := splitTags("Advance Wars (USA) (Rev 1) [!]")
	if strings.Join(tags, "|") != "USA|Rev 1|!" {
		t.Errorf("tags %v", tags)
	}
}

func TestScanMockCard(t *testing.T) {
	sd := os.Getenv("RR_TEST_SD")
	if sd == "" {
		t.Skip("set RR_TEST_SD to a mock card")
	}
	roms, systems := ScanRoms(sd, nil)
	by := map[string]*Rom{}
	for _, r := range roms {
		by[filepath.Base(r.Path)] = r
	}
	for _, bad := range []string{"._Metroid Fusion (USA).gba", "Castlevania - Symphony of the Night (USA).bin", "Final Fantasy VII (USA) (Disc 2).chd"} {
		if by[bad] != nil {
			t.Errorf("%s should be hidden", bad)
		}
	}
	for _, good := range []string{"Castlevania - Symphony of the Night (USA).cue", "Final Fantasy VII (USA).m3u", "Pokemon Unbound (v2.1).gba", "sf2.zip"} {
		if by[good] == nil {
			t.Errorf("%s missing", good)
		}
	}
	if r := by["Super Metroid (Japan, USA) (En,Ja).sfc"]; r == nil || r.Title != "Super Metroid" || r.Year != "1994" || r.Desc == "" || r.BoxArt() == "" {
		t.Errorf("gamelist/art not applied: %+v", r)
	}
	if r := by["sf2.zip"]; r.Title != "Street Fighter II: The World Warrior" {
		t.Errorf("arcade name %q", r.Title)
	}
	if r := by["Metroid Fusion (USA).gba"]; r.BoxArt() == "" {
		t.Error("no box art for Metroid Fusion")
	}
	t.Logf("%d roms across %d systems", len(roms), len(systems))

	// launching writes Onion's command and a Recents entry
	env := &Env{SDRoot: sd, DataDir: t.TempDir()}
	r := by["Metroid Fusion (USA).gba"]
	if err := QueueLaunch(env, r); err != nil {
		t.Fatal(err)
	}
	cmd, _ := os.ReadFile(filepath.Join(sd, ".tmp_update", "cmd_to_run.sh"))
	want := `LD_PRELOAD=/mnt/SDCARD/miyoo/app/../lib/libpadsp.so "` + sd + `/Emu/GBA/launch.sh" "` + sd + `/Roms/GBA/Metroid Fusion (USA).gba"`
	if string(cmd) != want {
		t.Errorf("cmd:\n%s\nwant\n%s", cmd, want)
	}
	QueueLaunch(env, r) // twice: still one Recents entry
	rec, _ := os.ReadFile(filepath.Join(sd, "Roms", "recentlist.json"))
	lines := strings.Split(strings.TrimSpace(string(rec)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "Metroid Fusion") || !strings.Contains(lines[0], `"type":5`) {
		t.Errorf("recents:\n%s", rec)
	}
	if _, err := os.Stat(filepath.Join(env.DataDir, ".launch")); err != nil {
		t.Error("no .launch marker")
	}
}
