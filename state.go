package main

// What ROM Roulette remembers between sessions: games launched from it,
// favourites, and per-game spin/play counts. Stored as data/state.json.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type PlayEntry struct {
	Path   string `json:"path"`
	Label  string `json:"label"`
	System string `json:"system"`
	Time   int64  `json:"time"`
}

type RomStats struct {
	Landed int   `json:"landed,omitempty"` // times the reel stopped on it
	Played int   `json:"played,omitempty"` // times launched from ROM Roulette
	Last   int64 `json:"last,omitempty"`
}

type FavEntry struct {
	Label  string `json:"label"`
	System string `json:"system"`
	Added  int64  `json:"added"`
}

type State struct {
	Spins     int                  `json:"spins"`
	History   []PlayEntry          `json:"history"` // newest first, one entry per game
	Favorites map[string]FavEntry  `json:"favorites"`
	Stats     map[string]*RomStats `json:"stats"`
	Pool      string               `json:"pool,omitempty"` // last reel filter
	Mute      bool                 `json:"mute,omitempty"`
	Ver       int                  `json:"ver,omitempty"`

	path string
}

func LoadState(dir string) *State {
	s := &State{Favorites: map[string]FavEntry{}, Stats: map[string]*RomStats{}, path: filepath.Join(dir, "state.json")}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, s)
	}
	if s.Favorites == nil {
		s.Favorites = map[string]FavEntry{}
	}
	if s.Ver < 2 {
		// 0.2.0–0.2.1 muted on START, which was easy to press by accident
		// ("start the game"); sound is on R2 now, so start everyone unmuted.
		s.Mute = false
		s.Ver = 2
	}
	if s.Stats == nil {
		s.Stats = map[string]*RomStats{}
	}
	return s
}

func (s *State) Save() error {
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *State) stat(path string) *RomStats {
	st := s.Stats[path]
	if st == nil {
		st = &RomStats{}
		s.Stats[path] = st
	}
	return st
}

func (s *State) Landed(r *Rom) {
	s.Spins++
	s.stat(r.Path).Landed++
}

func (s *State) Played(r *Rom) {
	now := time.Now().Unix()
	st := s.stat(r.Path)
	st.Played++
	st.Last = now
	h := []PlayEntry{{Path: r.Path, Label: r.Label, System: r.Sys.Label, Time: now}}
	for _, e := range s.History {
		if e.Path != r.Path {
			h = append(h, e)
		}
	}
	if len(h) > 500 {
		h = h[:500]
	}
	s.History = h
}

func (s *State) IsFav(path string) bool {
	_, ok := s.Favorites[path]
	return ok
}

func (s *State) ToggleFav(r *Rom) bool {
	if s.IsFav(r.Path) {
		delete(s.Favorites, r.Path)
		return false
	}
	s.Favorites[r.Path] = FavEntry{Label: r.Label, System: r.Sys.Label, Added: time.Now().Unix()}
	return true
}

// FavList returns favourites newest first.
func (s *State) FavList() []string {
	var out []string
	for p := range s.Favorites {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := s.Favorites[out[i]], s.Favorites[out[j]]
		if a.Added != b.Added {
			return a.Added > b.Added
		}
		return a.Label < b.Label
	})
	return out
}
