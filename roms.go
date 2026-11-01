package main

// Finds every ROM the user's installed OnionOS emulators can see, the same way
// MainUI does: each /mnt/SDCARD/Emu/<SYS>/config.json names a rompath and an
// extlist, and only those folders/extensions count.

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type System struct {
	ID     string // Emu folder name, e.g. "GBA"
	Label  string // config.json label, e.g. "GBA"
	Name   string // friendly name, e.g. "Game Boy Advance"
	Launch string // absolute path of the emulator's launch.sh
	RomDir string // absolute rom folder
	ImgDir string // absolute box-art folder
	Count  int
}

type Rom struct {
	Path    string // absolute path on the SD card
	Label   string // display name (gamelist name, else file name without extension)
	Title   string // Label with (…) and […] tags removed
	Tags    []string
	Sys     *System
	Size    int64
	Img     string // box art path if one exists
	Desc    string // from a scraped gamelist.xml, if any
	Genre   string
	Year    string
	Dev     string
	Players string
}

var systemNames = map[string]string{
	"AMIGA": "Commodore Amiga", "ARCADE": "Arcade", "ATARI": "Atari 2600", "ATARIST": "Atari ST",
	"COLECO": "ColecoVision", "COMMODORE": "Commodore 64", "CPC": "Amstrad CPC", "CPS1": "Capcom CPS-1",
	"CPS2": "Capcom CPS-2", "CPS3": "Capcom CPS-3", "DOS": "MS-DOS", "FAIRCHILD": "Fairchild Channel F",
	"FC": "Famicom / NES", "FDS": "Famicom Disk System", "FIFTYTWOHUNDRED": "Atari 5200",
	"GB": "Game Boy", "GBA": "Game Boy Advance", "GBC": "Game Boy Color", "GG": "Game Gear",
	"GW": "Game & Watch", "INTELLIVISION": "Intellivision", "LYNX": "Atari Lynx", "MD": "Mega Drive / Genesis",
	"MEGADUCK": "Mega Duck", "MS": "Master System", "MSX": "MSX", "NDS": "Nintendo DS",
	"NEOCD": "Neo Geo CD", "NEOGEO": "Neo Geo", "NGP": "Neo Geo Pocket", "ODYSSEY": "Magnavox Odyssey 2",
	"PCE": "PC Engine / TG-16", "PCECD": "PC Engine CD", "PICO": "PICO-8", "POKE": "Pokémon Mini",
	"PORTS": "Ports", "PS": "PlayStation", "PSX": "PlayStation", "SATELLAVIEW": "Satellaview",
	"SCUMMVM": "ScummVM", "SEGACD": "Sega CD", "SEGASGONE": "Sega SG-1000", "SEVENTYEIGHTHUNDRED": "Atari 7800",
	"SFC": "Super Famicom / SNES", "SGB": "Super Game Boy", "SGFX": "SuperGrafx", "SUFAMI": "Sufami Turbo",
	"SUPERVISION": "Watara Supervision", "THIRTYTWOX": "Sega 32X", "TIC": "TIC-80", "VB": "Virtual Boy",
	"VECTREX": "Vectrex", "VIC20": "Commodore VIC-20", "VIDEOPAC": "Videopac", "WS": "WonderSwan",
	"ZXS": "ZX Spectrum", "N64": "Nintendo 64", "PSP": "PSP", "EASYRPG": "EasyRPG", "MAME": "MAME",
}

type emuConfig struct {
	Label    string `json:"label"`
	Launch   string `json:"launch"`
	RomPath  string `json:"rompath"`
	ImgPath  string `json:"imgpath"`
	Gamelist string `json:"gamelist"`
	ExtList  string `json:"extlist"`
}

// ScanRoms walks every installed emulator. progress (may be nil) is called
// with a running total so the loading screen can count up.
func ScanRoms(sd string, progress func(n int)) ([]*Rom, []*System) {
	emuDir := filepath.Join(sd, "Emu")
	ents, _ := os.ReadDir(emuDir)
	var roms []*Rom
	var systems []*System
	seenDir := map[string]bool{}
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(emuDir, e.Name())
		b, err := os.ReadFile(filepath.Join(dir, "config.json"))
		if err != nil {
			continue
		}
		var cfg emuConfig
		if json.Unmarshal(b, &cfg) != nil || cfg.RomPath == "" || cfg.ExtList == "" {
			continue
		}
		launch := cfg.Launch
		if launch == "" {
			launch = "launch.sh"
		}
		sys := &System{
			ID:     e.Name(),
			Label:  strings.TrimSpace(cfg.Label),
			Launch: filepath.Join(dir, launch),
			RomDir: resolve(dir, cfg.RomPath),
		}
		if _, err := os.Stat(sys.Launch); err != nil {
			continue
		}
		if seenDir[sys.RomDir] {
			continue
		}
		seenDir[sys.RomDir] = true
		if cfg.ImgPath != "" {
			sys.ImgDir = resolve(dir, cfg.ImgPath)
		} else {
			sys.ImgDir = filepath.Join(sys.RomDir, "Imgs")
		}
		sys.Name = systemNames[strings.ToUpper(sys.ID)]
		if sys.Name == "" {
			sys.Name = systemNames[strings.ToUpper(filepath.Base(sys.RomDir))]
		}
		if sys.Name == "" {
			sys.Name = sys.Label
		}
		if sys.Label == "" {
			sys.Label = sys.ID
		}
		exts := map[string]bool{}
		for _, x := range strings.Split(strings.ToLower(cfg.ExtList), "|") {
			if x = strings.TrimSpace(x); x != "" && x != "miyoocmd" {
				exts[x] = true
			}
		}
		meta := readGamelists(sys.RomDir, resolveOpt(dir, cfg.Gamelist))
		found := scanDir(sys, sys.RomDir, exts, meta, 0)
		if len(found) == 0 {
			continue
		}
		sys.Count = len(found)
		roms = append(roms, found...)
		systems = append(systems, sys)
		if progress != nil {
			progress(len(roms))
		}
	}
	sort.Slice(systems, func(i, j int) bool { return systems[i].Name < systems[j].Name })
	return roms, systems
}

func resolve(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

func resolveOpt(base, p string) string {
	if p == "" {
		return ""
	}
	return resolve(base, p)
}

var skipDirs = map[string]bool{"imgs": true, ".game_config": true, "media": true, "images": true, "videos": true, "manuals": true, "bios": true}

func scanDir(sys *System, dir string, exts map[string]bool, meta map[string]gameMeta, depth int) []*Rom {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	// Multi-disc sets: hide the .bin halves of .cue sheets and anything an .m3u lists.
	hidden := map[string]bool{}
	hasCue := map[string]bool{}
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		switch filepath.Ext(n) {
		case ".m3u":
			if exts["m3u"] {
				if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil && len(b) < 64<<10 {
					for _, ln := range strings.Split(string(b), "\n") {
						ln = strings.TrimSpace(strings.ReplaceAll(ln, "\\", "/"))
						if ln != "" && !strings.HasPrefix(ln, "#") {
							hidden[strings.ToLower(filepath.Clean(filepath.Join(dir, ln)))] = true
						}
					}
				}
			}
		case ".cue":
			hasCue[dir] = true
		}
	}
	var out []*Rom
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // includes macOS ._ files
		}
		full := filepath.Join(dir, name)
		if e.IsDir() {
			if depth < 3 && !skipDirs[strings.ToLower(name)] {
				out = append(out, scanDir(sys, full, exts, meta, depth+1)...)
			}
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		if !exts[ext] || hidden[strings.ToLower(full)] {
			continue
		}
		if ext == "bin" && hasCue[dir] && exts["cue"] {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		r := &Rom{Path: full, Sys: sys, Label: stem}
		if info, err := e.Info(); err == nil {
			r.Size = info.Size()
		}
		if m, ok := meta[strings.ToLower(full)]; ok {
			if m.Name != "" {
				r.Label = m.Name
			}
			r.Desc, r.Genre, r.Year, r.Dev, r.Players = m.Desc, m.Genre, m.Year, m.Dev, m.Players
			if m.Image != "" {
				if _, err := os.Stat(m.Image); err == nil {
					r.Img = m.Image
				}
			}
		}
		r.Title, r.Tags = splitTags(r.Label)
		out = append(out, r)
	}
	return out
}

// BoxArt finds the rom's picture the way MainUI does (Imgs/<stem>.png), falling
// back to whatever a scraped gamelist pointed at.
func (r *Rom) BoxArt() string {
	if r.Img != "" {
		return r.Img
	}
	stem := strings.TrimSuffix(filepath.Base(r.Path), filepath.Ext(r.Path))
	rel, err := filepath.Rel(r.Sys.RomDir, filepath.Dir(r.Path))
	if err != nil {
		rel = "."
	}
	for _, d := range []string{filepath.Join(r.Sys.ImgDir, rel), r.Sys.ImgDir} {
		for _, x := range []string{".png", ".jpg", ".jpeg"} {
			p := filepath.Join(d, stem+x)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

var tagRe = regexp.MustCompile(`\s*[\(\[]([^\)\]]*)[\)\]]`)

// splitTags turns "Pokemon - Emerald Version (USA, Europe) [!]" into the
// title "Pokemon - Emerald Version" and tags [USA Europe !].
func splitTags(label string) (string, []string) {
	var tags []string
	for _, m := range tagRe.FindAllStringSubmatch(label, -1) {
		for _, t := range strings.Split(m[1], ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}
	title := strings.TrimSpace(tagRe.ReplaceAllString(label, ""))
	if title == "" {
		title = label
	}
	// "Legend of Zelda, The" → "The Legend of Zelda"
	for _, art := range []string{", The", ", A", ", An"} {
		if i := strings.Index(title, art); i > 0 && (i+len(art) == len(title) || title[i+len(art)] == ' ') {
			title = art[2:] + " " + title[:i] + title[i+len(art):]
			break
		}
	}
	return title, tags
}

// ---------- gamelists (Onion's miyoogamelist.xml and scraped gamelist.xml) ----------

type gameMeta struct {
	Name, Image, Desc, Genre, Year, Dev, Players string
}

type xmlGame struct {
	Path        string `xml:"path"`
	Name        string `xml:"name"`
	Image       string `xml:"image"`
	Desc        string `xml:"desc"`
	Genre       string `xml:"genre"`
	ReleaseDate string `xml:"releasedate"`
	Developer   string `xml:"developer"`
	Players     string `xml:"players"`
}

type xmlList struct {
	Games []xmlGame `xml:"game"`
}

func readGamelists(romDir, cfgList string) map[string]gameMeta {
	out := map[string]gameMeta{}
	paths := []string{filepath.Join(romDir, "gamelist.xml"), filepath.Join(romDir, "miyoogamelist.xml")}
	if cfgList != "" && cfgList != paths[1] {
		paths = append(paths, cfgList)
	}
	for _, p := range paths { // later files win for names; desc etc. kept from the first that has them
		b, err := os.ReadFile(p)
		if err != nil || len(b) > 32<<20 {
			continue
		}
		var gl xmlList
		if xml.Unmarshal(b, &gl) != nil {
			continue
		}
		base := filepath.Dir(p)
		for _, g := range gl.Games {
			if g.Path == "" {
				continue
			}
			key := strings.ToLower(resolve(base, g.Path))
			m := out[key]
			if s := strings.TrimSpace(g.Name); s != "" {
				m.Name = s
			}
			if g.Image != "" {
				m.Image = resolve(base, g.Image)
			}
			if m.Desc == "" {
				m.Desc = strings.TrimSpace(g.Desc)
			}
			if m.Genre == "" {
				m.Genre = strings.TrimSpace(g.Genre)
			}
			if m.Year == "" && len(g.ReleaseDate) >= 4 {
				m.Year = g.ReleaseDate[:4]
			}
			if m.Dev == "" {
				m.Dev = strings.TrimSpace(g.Developer)
			}
			if m.Players == "" {
				m.Players = strings.TrimSpace(g.Players)
			}
			out[key] = m
		}
	}
	return out
}
