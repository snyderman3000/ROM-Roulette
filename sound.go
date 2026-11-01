package main

// Sound effects run in a helper process, rrsfx (sfx/rrsfx.c). OnionOS carries
// game and menu audio to its audioserver through an LD_PRELOAD library
// (miyoo/lib/libpadsp.so) that a static Go binary can't use, so the helper —
// a normal dynamically linked program — is started with it preloaded, and the
// app sends it one-line commands over a pipe.

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Sound struct {
	mu    sync.Mutex
	cmd   *exec.Cmd
	done  chan struct{} // closed when the helper exits
	in    io.WriteCloser
	lens  map[string]float64 // seconds
	Muted bool
}

// padspCandidates mirrors MiniAmp: Onion's own library first, skipping
// MiniAmp's music-in-games wrapper if it has taken libpadsp.so's place.
var padspCandidates = []string{
	"/mnt/SDCARD/miyoo/lib/libpadsp_orig.so",
	"/mnt/SDCARD/miyoo/lib/libpadsp.so",
	"/mnt/SDCARD/App/MiniAmp/data/libpadsp_onion.so",
	"/mnt/SDCARD/.tmp_update/lib/libpadsp.so",
	"/customer/lib/libpadsp.so",
}

func findPadsp() string {
	for _, p := range padspCandidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if bytes.Contains(b, []byte("MINIAMP_PADSP_WRAPPER")) {
			continue
		}
		return p
	}
	return ""
}

// StartSound launches the mixer. It returns a usable (silent) Sound even when
// the helper is missing, so callers never need to check.
func StartSound(appDir string) *Sound {
	s := &Sound{lens: map[string]float64{}}
	dir := filepath.Join(appDir, "sfx")
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".raw") {
			if info, err := e.Info(); err == nil {
				s.lens[strings.TrimSuffix(e.Name(), ".raw")] = float64(info.Size()) / 2 / 44100
			}
		}
	}
	bin := os.Getenv("RR_SFX_BIN")
	if bin == "" {
		bin = filepath.Join(appDir, "rrsfx")
	}
	if _, err := os.Stat(bin); err != nil {
		log.Printf("sound: no helper (%v)", err)
		return s
	}
	cmd := exec.Command(bin, dir)
	cmd.Env = os.Environ()
	if os.Getenv("RRSFX_WAV") == "" {
		if p := findPadsp(); p != "" {
			cmd.Env = append(cmd.Env, "LD_PRELOAD="+p)
			log.Printf("sound: LD_PRELOAD=%s", p)
		} else {
			log.Printf("sound: no libpadsp.so found; trying /dev/dsp directly")
		}
	}
	lf, _ := os.OpenFile(filepath.Join(appDir, "data", "sound.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if lf != nil {
		cmd.Stdout, cmd.Stderr = lf, lf
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return s
	}
	if err := cmd.Start(); err != nil {
		log.Printf("sound: %v", err)
		return s
	}
	s.cmd, s.in = cmd, in
	s.done = make(chan struct{})
	go func() {
		err := cmd.Wait()
		msg := "rrsfx exited normally"
		if err != nil {
			msg = "rrsfx exited: " + err.Error()
		}
		log.Printf("sound: %s", msg)
		if lf != nil {
			fmt.Fprintln(lf, msg)
			lf.Close()
		}
		s.mu.Lock()
		if s.in != nil {
			s.in.Close()
			s.in = nil
		}
		s.mu.Unlock()
		close(s.done)
	}()
	return s
}

func (s *Sound) send(line string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.in == nil {
		return
	}
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		log.Printf("sound: helper gone (%v)", err)
		s.in.Close()
		s.in = nil
	}
}

// Play starts a sound; copies may overlap.
func (s *Sound) Play(name string) {
	if s != nil && !s.Muted {
		s.send("p " + name)
	}
}

// Excl starts a sound, cutting off an earlier copy of the same one (reel ticks).
func (s *Sound) Excl(name string) {
	if s != nil && !s.Muted {
		s.send("x " + name)
	}
}

func (s *Sound) StopAll() { s.send("s") }

func (s *Sound) SetMuted(m bool) {
	if s == nil {
		return
	}
	s.Muted = m
	if m {
		s.send("s")
	}
}

// Len is how long a sound lasts, in seconds (0 if unknown or muted).
func (s *Sound) Len(name string) float64 {
	if s == nil || s.Muted || s.in == nil {
		return 0
	}
	return s.lens[name]
}

// Close lets the helper finish and release the audio device — before a game
// starts and wants it.
func (s *Sound) Close() {
	if s == nil || s.cmd == nil {
		return
	}
	s.mu.Lock()
	if s.in != nil {
		s.in.Close()
		s.in = nil
	}
	s.mu.Unlock()
	select {
	case <-s.done:
	case <-time.After(1500 * time.Millisecond):
		s.cmd.Process.Kill()
		<-s.done
	}
}

func (s *Sound) String() string {
	if s == nil || s.cmd == nil {
		return "sound: off"
	}
	return fmt.Sprintf("sound: pid %d, %d effects", s.cmd.Process.Pid, len(s.lens))
}
