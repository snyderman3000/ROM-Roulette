package main

import "time"

// demoScene puts the UI into a given state for -shot screenshots.
func demoScene(u *UI, name string) {
	u.t = 10.0
	if name == "loading" {
		u.scr, u.loaded = scrLoading, 742
		return
	}
	if name == "empty" {
		u.SetRoms(nil, nil)
		return
	}
	roms, systems := ScanRoms(u.env.SDRoot, nil)
	u.SetRoms(roms, systems)
	for i := -6; i < len(u.roms)+6; i++ { // deterministic reel for screenshots
		u.rows[i] = u.roms[((i%len(u.roms))+len(u.roms))%len(u.roms)]
	}
	now := time.Now().Unix()
	switch name {
	case "idle":
		u.pos, u.t = 3, 10.4
	case "spin":
		u.ph, u.v, u.pos, u.lastMove = phSpinning, vMax, 12.4, vMax/30
		u.leverT = u.t - 0.18
	case "stopping":
		u.ph, u.v, u.pos, u.lastMove = phStopping, 6, 20.3, 6.0/30
	case "landed":
		u.ph, u.pos, u.phT = phLanded, 7, u.t-0.3
		u.t = 10.0
	case "card":
		u.ph, u.pos = phLanded, 7
		u.st.Stats[u.at(7).Path] = &RomStats{Landed: 3, Played: 1, Last: now - 3600*26}
		u.openCard(u.at(7), scrMachine)
	case "card2":
		for i, r := range u.roms {
			if r.Desc != "" && r.BoxArt() != "" {
				u.pos = float64(i)
				u.st.Favorites[r.Path] = FavEntry{r.Label, r.Sys.Label, now}
				u.openCard(r, scrMachine)
			}
		}
	case "card3":
		for i, r := range u.roms {
			if r.Desc == "" && r.BoxArt() != "" {
				u.pos = float64(i)
				u.openCard(r, scrList)
				break
			}
		}
	case "played", "faves":
		u.st.History = nil
		for i, r := range u.roms {
			if i%3 == 0 {
				u.st.History = append(u.st.History, PlayEntry{r.Path, r.Label, r.Sys.Label, now - int64(i)*40000})
			}
			if i%4 == 1 {
				u.st.Favorites[r.Path] = FavEntry{r.Label, r.Sys.Label, now - int64(i)*90000}
			}
		}
		u.st.History = append(u.st.History, PlayEntry{"/mnt/SDCARD/Roms/GB/Gone (USA).gb", "Gone (USA)", "GB", now - 9e6})
		tab := 0
		if name == "faves" {
			tab = 1
		}
		u.openList(tab)
		u.sel = 2
	}
}
